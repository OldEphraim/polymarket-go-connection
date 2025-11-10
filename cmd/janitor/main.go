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
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type TableConfig struct {
	window          string
	emergencyWindow string
	emergencyBatch  int
	keepHours       int
}

func loadDynamicPolicy(defaultWindow, defaultTradesWindow, defaultFeaturesWindow string) (string, string, string, int) {
	policyFile := "/tmp/retention_policy.json"
	data, err := os.ReadFile(policyFile)
	if err != nil {
		log.Printf("[janitor] Using defaults, no policy file: %v", err)
		return defaultWindow, defaultTradesWindow, defaultFeaturesWindow, 3000
	}

	var policy struct {
		QuotesHours        float64 `json:"quotes_hours"`
		TradesHours        float64 `json:"trades_hours"`
		FeaturesHours      float64 `json:"features_hours"`
		EmergencyThreshold int     `json:"emergency_threshold_mb"`
		JanitorSleepSecs   int     `json:"janitor_sleep_secs"`
		BacklogQuotesH     float64 `json:"backlog_quotes_h"`
		BacklogTradesH     float64 `json:"backlog_trades_h"`
		BacklogFeaturesH   float64 `json:"backlog_features_h"`
	}

	if err := json.Unmarshal(data, &policy); err != nil {
		log.Printf("[janitor] Using defaults, bad policy file: %v", err)
		return defaultWindow, defaultTradesWindow, defaultFeaturesWindow, 3000
	}

	quotesWindow := fmt.Sprintf("%.1f hours", policy.QuotesHours)
	tradesWindow := fmt.Sprintf("%.1f hours", policy.TradesHours)
	featuresWindow := fmt.Sprintf("%.1f hours", policy.FeaturesHours)

	log.Printf("[janitor] Loaded dynamic policy: quotes=%s trades=%s features=%s emergency=%dMB sleep=%ds backlog(h): q=%.1f t=%.1f f=%.1f",
		quotesWindow, tradesWindow, featuresWindow, policy.EmergencyThreshold,
		policy.JanitorSleepSecs, policy.BacklogQuotesH, policy.BacklogTradesH, policy.BacklogFeaturesH)

	return quotesWindow, tradesWindow, featuresWindow, policy.EmergencyThreshold
}

func main() {
	var (
		quotesWindow   = flag.String("quotes_window", "2 hours", "default hot window for quotes")
		tradesWindow   = flag.String("trades_window", "12 hours", "default hot window for trades")
		featuresWindow = flag.String("features_window", "6 hours", "default hot window for features")
		tables         = flag.String("tables", "market_features,market_trades,market_quotes", "comma list of table names")

		batchQuotes    = flag.Int("batch_quotes", 100000, "delete batch size for quotes in emergency")
		batchTrades    = flag.Int("batch_trades", 50000, "delete batch size for trades in emergency")
		batchFeatures  = flag.Int("batch_features", 50000, "delete batch size for features in emergency")
		emergencyGrace = flag.Duration("emergency_sleep", 5*time.Second, "sleep between emergency batches")

		precreateHoursBack = flag.Int("precreate_hours_back", 2, "precreate partitions hours back")
		precreateHoursFwd  = flag.Int("precreate_hours_fwd", 2, "precreate partitions hours forward")

		checkInterval = flag.Duration("check_interval", 5*time.Minute, "fallback: how often to reload policy and run cleanup (overridden by policy)")
	)
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	q := database.New(db)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)

		dynQuotes, dynTrades, dynFeatures, dynEmergencyMB :=
			loadDynamicPolicy(*quotesWindow, *tradesWindow, *featuresWindow)

		tableConfigs := map[string]*TableConfig{
			"market_features": {
				window:          dynFeatures,
				emergencyWindow: dynFeatures,
				emergencyBatch:  *batchFeatures,
				keepHours:       int(math.Ceil(parseHours(dynFeatures))), // ceil for safety
			},
			"market_trades": {
				window:          dynTrades,
				emergencyWindow: dynTrades,
				emergencyBatch:  *batchTrades,
				keepHours:       int(math.Ceil(parseHours(dynTrades))),
			},
			"market_quotes": {
				window:          dynQuotes,
				emergencyWindow: dynQuotes,
				emergencyBatch:  *batchQuotes,
				keepHours:       int(math.Ceil(parseHours(dynQuotes))),
			},
		}

		// Run partition drops first (fast, big wins), then windowed deletes
		runJanitorCycle(ctx, q, tableConfigs, *tables, dynEmergencyMB,
			*emergencyGrace, *precreateHoursBack, *precreateHoursFwd)

		cancel()
		// Prefer dynamic cadence from policy file when present
		if b, err := os.ReadFile("/tmp/retention_policy.json"); err == nil {
			var p struct {
				JanitorSleepSecs int `json:"janitor_sleep_secs"`
			}
			if json.Unmarshal(b, &p) == nil && p.JanitorSleepSecs > 0 {
				time.Sleep(time.Duration(p.JanitorSleepSecs) * time.Second)
				continue
			}
		}
		time.Sleep(*checkInterval)
	}
}

func parseHours(window string) float64 {
	var hours float64
	fmt.Sscanf(window, "%f hours", &hours)
	if hours <= 0 {
		hours = 1
	}
	return hours
}

func runJanitorCycle(
	ctx context.Context,
	q *database.Queries,
	tableConfigs map[string]*TableConfig,
	tablesCSV string,
	minFreeMB int,
	emergencyGrace time.Duration,
	precreateBack, precreateFwd int,
) {
	// Ensure partitions (typed, per-table).
	if err := q.EnsureFeaturesPartitionsHourly(ctx, database.EnsureFeaturesPartitionsHourlyParams{
		Back: int32(precreateBack), Fwd: int32(precreateFwd),
	}); err != nil {
		log.Printf("[janitor] ensure_features_partitions_hourly failed: %v", err)
	}
	if err := q.EnsureTradesPartitionsHourly(ctx, database.EnsureTradesPartitionsHourlyParams{
		Back: int32(precreateBack), Fwd: int32(precreateFwd),
	}); err != nil {
		log.Printf("[janitor] ensure_trades_partitions_hourly failed: %v", err)
	}
	if err := q.EnsureQuotesPartitionsHourly(ctx, database.EnsureQuotesPartitionsHourlyParams{
		Back: int32(precreateBack), Fwd: int32(precreateFwd),
	}); err != nil {
		log.Printf("[janitor] ensure_quotes_partitions_hourly failed: %v", err)
	}

	free := freeMB("/")
	log.Printf("[janitor] free_mb=%d min_free_mb=%d", free, minFreeMB)

	// Emergency mode: aggressively free space in quotes -> trades -> features order.
	if free < minFreeMB {
		log.Printf("[janitor] EMERGENCY MODE: low disk")
		deadline := time.Now().Add(5 * time.Minute)
		round := 0

		order := []string{"market_quotes", "market_trades", "market_features"}

		for {
			round++
			var total int64

			for _, t := range order {
				cfg, ok := tableConfigs[t]
				if !ok {
					continue
				}
				deleted := emergencyDeleteOnce(ctx, q, t, cfg.emergencyWindow, cfg.emergencyBatch)
				total += deleted
			}

			dbCheckpoint(ctx, q)
			free = freeMB("/")
			log.Printf("[janitor] round=%d total_deleted=%d free_mb=%d", round, total, free)

			if free >= minFreeMB || total == 0 || time.Now().After(deadline) || round >= 10 {
				break
			}
			time.Sleep(emergencyGrace)
		}
	}

	// Normal mode: DROP archived partitions first (fast O(1)), then windowed deletes
	for _, tableName := range splitCSV(tablesCSV) {
		cfg, ok := tableConfigs[tableName]
		if !ok {
			log.Printf("skip unknown table: %s", tableName)
			continue
		}
		switch tableName {
		case "market_quotes":
			if dropped, err := q.DropArchivedMarketQuotesPartitionsHourly(ctx, int32(cfg.keepHours)); err != nil {
				log.Printf("[janitor] drop_archived quotes failed: %v", err)
			} else if dropped > 0 {
				log.Printf("[janitor] dropped %d %s partitions", dropped, tableName)
				dbCheckpoint(ctx, q)
			}
		case "market_trades":
			if dropped, err := q.DropArchivedMarketTradesPartitionsHourly(ctx, int32(cfg.keepHours)); err != nil {
				log.Printf("[janitor] drop_archived trades failed: %v", err)
			} else if dropped > 0 {
				log.Printf("[janitor] dropped %d %s partitions", dropped, tableName)
				dbCheckpoint(ctx, q)
			}
		case "market_features":
			if dropped, err := q.DropArchivedMarketFeaturesPartitionsHourly(ctx, int32(cfg.keepHours)); err != nil {
				log.Printf("[janitor] drop_archived features failed: %v", err)
			} else if dropped > 0 {
				log.Printf("[janitor] dropped %d %s partitions", dropped, tableName)
				dbCheckpoint(ctx, q)
			}
		}
	}

	// Then do windowed deletes (rows that are archived but still inside the keep window)
	for _, tableName := range splitCSV(tablesCSV) {
		cfg, ok := tableConfigs[tableName]
		if !ok {
			log.Printf("skip unknown table: %s", tableName)
			continue
		}

		var deleted int64
		var err error

		switch tableName {
		case "market_quotes":
			deleted, err = q.DeleteExportedHoursQuotes(ctx, cfg.window)
		case "market_trades":
			deleted, err = q.DeleteExportedHoursTrades(ctx, cfg.window)
		case "market_features":
			deleted, err = q.DeleteExportedHoursFeatures(ctx, cfg.window)
		default:
			log.Printf("skip unknown table: %s", tableName)
		}

		if err != nil {
			log.Printf("[janitor] delete_exported_hours for %s failed: %v", tableName, err)
		} else {
			log.Printf("[janitor] %s delete_exported_hours(%q) -> %d", tableName, cfg.window, deleted)
			if deleted > 10000 {
				vacuumTable(ctx, q, tableName)
			}
		}
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func emergencyDeleteOnce(ctx context.Context, q *database.Queries, table, keep string, batch int) int64 {
	switch table {
	case "market_quotes":
		n, err := q.EmergencyDeleteQuotes(ctx, database.EmergencyDeleteQuotesParams{
			KeepText: keep, // ← string like "6 hours"
			Batch:    int32(batch),
		})
		if err != nil {
			log.Printf("[janitor] emergency delete quotes failed: %v", err)
			return 0
		}
		return n
	case "market_trades":
		n, err := q.EmergencyDeleteTrades(ctx, database.EmergencyDeleteTradesParams{
			KeepText: keep,
			Batch:    int32(batch),
		})
		if err != nil {
			log.Printf("[janitor] emergency delete trades failed: %v", err)
			return 0
		}
		return n
	case "market_features":
		n, err := q.EmergencyDeleteFeatures(ctx, database.EmergencyDeleteFeaturesParams{
			KeepText: keep,
			Batch:    int32(batch),
		})
		if err != nil {
			log.Printf("[janitor] emergency delete features failed: %v", err)
			return 0
		}
		return n
	default:
		return 0
	}
}

func vacuumTable(ctx context.Context, q *database.Queries, table string) {
	switch table {
	case "market_quotes":
		if err := q.VacuumQuotes(ctx); err != nil {
			log.Printf("vacuum failed for %s: %v", table, err)
		} else {
			log.Printf("vacuumed %s successfully", table)
		}
	case "market_trades":
		if err := q.VacuumTrades(ctx); err != nil {
			log.Printf("vacuum failed for %s: %v", table, err)
		} else {
			log.Printf("vacuumed %s successfully", table)
		}
	case "market_features":
		if err := q.VacuumFeatures(ctx); err != nil {
			log.Printf("vacuum failed for %s: %v", table, err)
		} else {
			log.Printf("vacuumed %s successfully", table)
		}
	}
}

func freeMB(path string) int {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int((stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024))
}

func dbCheckpoint(ctx context.Context, q *database.Queries) {
	if err := q.PgCheckpoint(ctx); err != nil {
		log.Printf("[janitor] CHECKPOINT failed: %v", err)
	}
	if lsn, err := q.PgSwitchWal(ctx); err != nil {
		log.Printf("[janitor] pg_switch_wal failed: %v", err)
	} else {
		_ = lsn // ignore or log if you want
	}
}
