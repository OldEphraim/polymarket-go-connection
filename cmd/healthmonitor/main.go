package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	_ "github.com/lib/pq"
)

type HealthMetrics struct {
	Timestamp       time.Time `json:"timestamp"`
	FreeSpaceMB     int       `json:"free_space_mb"`
	TotalSpaceMB    int       `json:"total_space_mb"`
	UsedPercent     float64   `json:"used_percent"`
	WriteRateMBHour float64   `json:"write_rate_mb_hour"`
	PartitionCount  int       `json:"partition_count"`
	OldestPartition time.Time `json:"oldest_partition"`
	NewestPartition time.Time `json:"newest_partition"`
}

type RetentionPolicy struct {
	Timestamp          time.Time `json:"timestamp"`
	FeaturesHours      float64   `json:"features_hours"`
	TradesHours        float64   `json:"trades_hours"`
	QuotesHours        float64   `json:"quotes_hours"`
	ArchiveSleepSecs   int       `json:"archive_sleep_secs"`
	JanitorSleepSecs   int       `json:"janitor_sleep_secs"`
	EmergencyThreshold int       `json:"emergency_threshold_mb"`

	// NEW: dynamic pacing / backpressure signals
	BackpressureFreeMB int  `json:"backpressure_free_mb"`
	PauseGatherer      bool `json:"pause_gatherer"`
}

// Hysteresis thresholds (tweak as you like)
const (
	// gatherer pause/resume bands
	pauseUsedPctOn  = 90.0 // pause when used% >= 90
	pauseUsedPctOff = 85.0 // resume when used% <= 85

	// emergency free space bands (MB)
	emergencyFreeMBOn  = 1_500 // enter emergency if free < 1.5 GB
	emergencyFreeMBOff = 3_000 // exit emergency if free > 3.0 GB

	// default backpressure free space for writers (MB)
	defaultBackpressureFreeMB = 3_000

	minSafeguardWriteRate = 1.0 // MB/hour (avoid division by zero)
	minRetentionHours     = 0.1 // never go below this when clamping
)

func main() {
	var (
		dsn           = flag.String("db", "", "Database URL")
		targetUsedPct = flag.Float64("target", 70.0, "Target disk usage percentage")
		minRetention  = flag.Float64("min-retention", 0.5, "Minimum retention hours")
		maxRetention  = flag.Float64("max-retention", 12.0, "Maximum retention hours")
		checkInterval = flag.Duration("interval", 60*time.Second, "Check interval")
		policyFile    = flag.String("policy-file", "/tmp/retention_policy.json", "Policy output file")
	)
	flag.Parse()

	if *dsn == "" {
		*dsn = os.Getenv("DATABASE_URL")
	}
	if *dsn == "" {
		log.Fatal("DATABASE_URL required")
	}

	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()
	q := database.New(db)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		prev := readPrevPolicy(*policyFile)
		metrics := collectMetrics(ctx, q)
		policy := calculatePolicyStable(metrics, prev, *targetUsedPct, *minRetention, *maxRetention)

		// Write policy to file for other services to read
		if err := writePolicy(*policyFile, policy); err != nil {
			log.Printf("Failed to write policy: %v", err)
		}

		log.Printf("Metrics: FreeMB=%d UsedPct=%.1f%% WriteRate=%.1fMB/hr Partitions=%d",
			metrics.FreeSpaceMB, metrics.UsedPercent, metrics.WriteRateMBHour, metrics.PartitionCount)
		log.Printf("Policy: PauseGatherer=%v BackpressureFreeMB=%d Features=%.1fh Trades=%.1fh Quotes=%.1fh ArchiveSleep=%ds JanitorSleep=%ds EmergencyFreeMB=%d",
			policy.PauseGatherer, policy.BackpressureFreeMB,
			policy.FeaturesHours, policy.TradesHours, policy.QuotesHours,
			policy.ArchiveSleepSecs, policy.JanitorSleepSecs, policy.EmergencyThreshold)

		cancel()
		time.Sleep(*checkInterval)
	}
}

func collectMetrics(ctx context.Context, q *database.Queries) HealthMetrics {
	m := HealthMetrics{
		Timestamp: time.Now(),
	}

	// Disk space
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil && stat.Bsize > 0 {
		total := int(stat.Blocks * uint64(stat.Bsize) / 1024 / 1024)
		free := int(stat.Bavail * uint64(stat.Bsize) / 1024 / 1024)
		if total < 0 {
			total = 0
		}
		if free < 0 {
			free = 0
		}
		m.TotalSpaceMB = total
		m.FreeSpaceMB = free
		if total > 0 {
			usedPct := float64(total-free) / float64(total) * 100
			m.UsedPercent = math.Max(0, math.Min(100, usedPct))
		}
	}

	// Build LIKE pattern for the current year (e.g., market_%_p2025%)
	year := time.Now().Year()
	pattern := fmt.Sprintf("market_%%_p%d%%", year)

	// Partition span via direct query (sqlc returns interface{} for TABLE functions)
	span, err := q.GetPartitionSpan(ctx, pattern)
	if err == nil {
		m.PartitionCount = int(span.PartitionCount)
		if span.OldestPartition.Valid {
			m.OldestPartition = span.OldestPartition.Time
		}
		if span.NewestPartition.Valid {
			m.NewestPartition = span.NewestPartition.Time
		}
	}

	// Estimate write rate from span (clamped)
	if !m.OldestPartition.IsZero() && !m.NewestPartition.IsZero() {
		hoursDiff := m.NewestPartition.Sub(m.OldestPartition).Hours()
		if hoursDiff > 0 {
			// very rough: X partitions ~ X*100 MB (your earlier heuristic)
			m.WriteRateMBHour = float64(m.PartitionCount) * 100.0 / hoursDiff
		}
	}

	// Better write rate from actual sizes (clamped)
	totalSizeMB, _ := q.SumPartitionSizesMB(ctx, pattern)
	if !m.OldestPartition.IsZero() {
		hoursSinceOldest := time.Since(m.OldestPartition).Hours()
		if hoursSinceOldest > 0 {
			m.WriteRateMBHour = totalSizeMB / hoursSinceOldest
		}
	}

	// Final clamp on write rate
	if m.WriteRateMBHour <= 0 || math.IsNaN(m.WriteRateMBHour) || math.IsInf(m.WriteRateMBHour, 0) {
		m.WriteRateMBHour = minSafeguardWriteRate
	}

	return m
}

// Stable policy with hysteresis & clamped math
func calculatePolicyStable(m HealthMetrics, prev RetentionPolicy, targetPct, minHours, maxHours float64) RetentionPolicy {
	now := time.Now()
	if minHours < minRetentionHours {
		minHours = minRetentionHours
	}
	if maxHours < minHours {
		maxHours = minHours
	}

	p := RetentionPolicy{
		Timestamp: now,
	}

	// Calculate how much space we want to use
	var targetSpaceMB, currentSpaceMB, spaceAvailable float64
	if m.TotalSpaceMB > 0 {
		targetSpaceMB = float64(m.TotalSpaceMB) * (targetPct / 100.0)
		currentSpaceMB = float64(m.TotalSpaceMB - m.FreeSpaceMB)
		// +10% buffer
		spaceAvailable = (targetSpaceMB - currentSpaceMB) + (float64(m.TotalSpaceMB) * 0.10)
	}
	if spaceAvailable < 0 {
		spaceAvailable = 0
	}

	// Guard against 0 write-rate
	wr := m.WriteRateMBHour
	if wr < minSafeguardWriteRate {
		wr = minSafeguardWriteRate
	}

	hoursWeCanKeep := spaceAvailable / wr
	if math.IsNaN(hoursWeCanKeep) || math.IsInf(hoursWeCanKeep, 0) {
		hoursWeCanKeep = minHours
	}
	// Clamp to bounds
	if hoursWeCanKeep < minHours {
		hoursWeCanKeep = minHours
	} else if hoursWeCanKeep > maxHours {
		hoursWeCanKeep = maxHours
	}

	// Distribute retention across tables (quotes most aggressive, features least)
	p.QuotesHours = clamp(hoursWeCanKeep*0.2, minHours, maxHours)
	p.TradesHours = clamp(hoursWeCanKeep*0.3, minHours, maxHours)
	p.FeaturesHours = clamp(hoursWeCanKeep*0.5, minHours, maxHours)

	// Base sleep intervals by utilization
	switch {
	case m.UsedPercent >= 85:
		p.ArchiveSleepSecs = 30
		p.JanitorSleepSecs = 60
	case m.UsedPercent >= 70:
		p.ArchiveSleepSecs = 60
		p.JanitorSleepSecs = 120
	default:
		p.ArchiveSleepSecs = 300
		p.JanitorSleepSecs = 300
	}

	// Backpressure signal for writers (archiver/janitor already consume; gatherer will also read it)
	// If we have very little free space, tell writers to slow down aggressively.
	switch {
	case m.FreeSpaceMB < 1_000:
		p.BackpressureFreeMB = 4_000
	case m.FreeSpaceMB < 2_000:
		p.BackpressureFreeMB = 3_500
	case m.FreeSpaceMB < 3_000:
		p.BackpressureFreeMB = 3_000
	default:
		p.BackpressureFreeMB = defaultBackpressureFreeMB
	}

	// Stable emergency threshold with hysteresis (encode "are we in emergency?")
	wasEmergency := prev.EmergencyThreshold > 0 && m.FreeSpaceMB < prev.EmergencyThreshold
	inEmergency := wasEmergency

	// Enter emergency if we drop below ON band; exit only after OFF band
	if !wasEmergency && m.FreeSpaceMB < emergencyFreeMBOn {
		inEmergency = true
	}
	if wasEmergency && m.FreeSpaceMB > emergencyFreeMBOff {
		inEmergency = false
	}

	if inEmergency {
		p.EmergencyThreshold = emergencyFreeMBOff // keep a stable, higher target to exit emergency
		// Pausing gatherer at the same time reduces incoming pressure
		p.PauseGatherer = true
		// While in emergency, tighten service pacing a bit too
		if p.ArchiveSleepSecs > 60 {
			p.ArchiveSleepSecs = 60
		}
		if p.JanitorSleepSecs > 120 {
			p.JanitorSleepSecs = 120
		}
	} else {
		p.EmergencyThreshold = emergencyFreeMBOn // when healthy, lower bound to trigger emergency next time
		// Consider pausing gatherer if %used is extremely high, even if not emergency by freeMB
		if prev.PauseGatherer {
			// Apply hysteresis on utilization as well
			p.PauseGatherer = m.UsedPercent >= pauseUsedPctOff
		} else {
			p.PauseGatherer = m.UsedPercent >= pauseUsedPctOn
		}
	}

	return p
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func writePolicy(filename string, policy RetentionPolicy) error {
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func readPrevPolicy(filename string) RetentionPolicy {
	var prev RetentionPolicy
	b, err := os.ReadFile(filename)
	if err != nil || len(b) == 0 {
		return prev
	}
	_ = json.Unmarshal(b, &prev)
	return prev
}
