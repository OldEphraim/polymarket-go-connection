package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"log"
	"os"
	"syscall"
	"time"

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
}

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

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		metrics := collectMetrics(ctx, db)
		policy := calculatePolicy(metrics, *targetUsedPct, *minRetention, *maxRetention)

		// Write policy to file for other services to read
		if err := writePolicy(*policyFile, policy); err != nil {
			log.Printf("Failed to write policy: %v", err)
		}

		log.Printf("Metrics: FreeMB=%d UsedPct=%.1f%% WriteRate=%.1fMB/hr Partitions=%d",
			metrics.FreeSpaceMB, metrics.UsedPercent, metrics.WriteRateMBHour, metrics.PartitionCount)
		log.Printf("Policy: Features=%.1fh Trades=%.1fh Quotes=%.1fh ArchiveSleep=%ds",
			policy.FeaturesHours, policy.TradesHours, policy.QuotesHours, policy.ArchiveSleepSecs)

		cancel()
		time.Sleep(*checkInterval)
	}
}

func collectMetrics(ctx context.Context, db *sql.DB) HealthMetrics {
	m := HealthMetrics{
		Timestamp: time.Now(),
	}

	// Get disk space
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		m.TotalSpaceMB = int(stat.Blocks * uint64(stat.Bsize) / 1024 / 1024)
		m.FreeSpaceMB = int(stat.Bavail * uint64(stat.Bsize) / 1024 / 1024)
		m.UsedPercent = float64(m.TotalSpaceMB-m.FreeSpaceMB) / float64(m.TotalSpaceMB) * 100
	}

	// Get partition info
	var partitionCount int
	var oldest, newest time.Time

	err := db.QueryRowContext(ctx, `
		WITH partitions AS (
			SELECT 
				tablename,
				CASE 
					WHEN length(regexp_replace(tablename, '^market_\w+_p', '')) = 10 
					THEN to_timestamp(regexp_replace(tablename, '^market_\w+_p', ''), 'YYYYMMDDHH24')
					ELSE NULL
				END as partition_time
			FROM pg_tables 
			WHERE tablename LIKE 'market_%_p2025%'
		)
		SELECT 
			COUNT(*),
			MIN(partition_time),
			MAX(partition_time)
		FROM partitions
		WHERE partition_time IS NOT NULL
	`).Scan(&partitionCount, &oldest, &newest)

	if err == nil {
		m.PartitionCount = partitionCount
		m.OldestPartition = oldest
		m.NewestPartition = newest
	}

	// Estimate write rate from recent partitions
	if !oldest.IsZero() && !newest.IsZero() {
		hoursDiff := newest.Sub(oldest).Hours()
		if hoursDiff > 0 {
			// Rough estimate: assume each partition is ~100MB when full
			m.WriteRateMBHour = float64(partitionCount) * 100 / hoursDiff
		}
	}

	// Better write rate from actual sizes
	var totalSizeMB float64
	db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(pg_total_relation_size(schemaname||'.'||tablename))::float / 1024 / 1024, 0)
		FROM pg_tables 
		WHERE tablename LIKE 'market_%_p2025%'
	`).Scan(&totalSizeMB)

	if !oldest.IsZero() {
		hoursSinceOldest := time.Since(oldest).Hours()
		if hoursSinceOldest > 0 {
			m.WriteRateMBHour = totalSizeMB / hoursSinceOldest
		}
	}

	return m
}

func calculatePolicy(m HealthMetrics, targetPct, minHours, maxHours float64) RetentionPolicy {
	p := RetentionPolicy{
		Timestamp: time.Now(),
	}

	// Calculate how much space we want to use
	targetSpaceMB := float64(m.TotalSpaceMB) * (targetPct / 100)
	currentSpaceMB := float64(m.TotalSpaceMB - m.FreeSpaceMB)

	// Calculate hours of data we can afford
	spaceAvailable := targetSpaceMB - currentSpaceMB + (float64(m.TotalSpaceMB) * 0.1) // 10% buffer
	hoursWeCanKeep := spaceAvailable / m.WriteRateMBHour

	// Clamp to bounds
	if hoursWeCanKeep < minHours {
		hoursWeCanKeep = minHours
	} else if hoursWeCanKeep > maxHours {
		hoursWeCanKeep = maxHours
	}

	// Distribute retention across tables (quotes most aggressive, features least)
	p.QuotesHours = hoursWeCanKeep * 0.2   // 20% for quotes
	p.TradesHours = hoursWeCanKeep * 0.3   // 30% for trades
	p.FeaturesHours = hoursWeCanKeep * 0.5 // 50% for features

	// Ensure minimums
	if p.QuotesHours < minHours {
		p.QuotesHours = minHours
	}
	if p.TradesHours < minHours {
		p.TradesHours = minHours
	}
	if p.FeaturesHours < minHours {
		p.FeaturesHours = minHours
	}

	// Adjust service frequencies based on pressure
	if m.UsedPercent > 80 {
		p.ArchiveSleepSecs = 30                     // Archive aggressively
		p.JanitorSleepSecs = 60                     // Clean aggressively
		p.EmergencyThreshold = m.FreeSpaceMB + 1000 // Force emergency mode
	} else if m.UsedPercent > 70 {
		p.ArchiveSleepSecs = 60
		p.JanitorSleepSecs = 120
		p.EmergencyThreshold = 3000
	} else {
		p.ArchiveSleepSecs = 300
		p.JanitorSleepSecs = 300
		p.EmergencyThreshold = 2000
	}

	return p
}

func writePolicy(filename string, policy RetentionPolicy) error {
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}
