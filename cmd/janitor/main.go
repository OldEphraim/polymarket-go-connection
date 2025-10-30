package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

// TableConfig holds configuration for each table's cleanup
type TableConfig struct {
	window            string // retention window
	deleteFn          string // SQL function to delete archived data
	dropPartitionFn   string // SQL function to drop partitions
	ensurePartitionFn string // SQL function to ensure partitions exist
	emergencyWindow   string // retention during emergency
	emergencyBatch    int    // batch size for emergency deletes
	keepHours         int    // hours to keep for partition dropping
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
	}

	if err := json.Unmarshal(data, &policy); err != nil {
		log.Printf("[janitor] Using defaults, bad policy file: %v", err)
		return defaultWindow, defaultTradesWindow, defaultFeaturesWindow, 3000
	}

	// Convert hours to interval strings
	quotesWindow := fmt.Sprintf("%.1f hours", policy.QuotesHours)
	tradesWindow := fmt.Sprintf("%.1f hours", policy.TradesHours)
	featuresWindow := fmt.Sprintf("%.1f hours", policy.FeaturesHours)

	log.Printf("[janitor] Loaded dynamic policy: quotes=%s trades=%s features=%s emergency=%dMB",
		quotesWindow, tradesWindow, featuresWindow, policy.EmergencyThreshold)

	return quotesWindow, tradesWindow, featuresWindow, policy.EmergencyThreshold
}

func main() {
	var (
		// Default windows (used only if no policy file exists)
		quotesWindow   = flag.String("quotes_window", "2 hours", "default hot window for quotes")
		tradesWindow   = flag.String("trades_window", "12 hours", "default hot window for trades")
		featuresWindow = flag.String("features_window", "6 hours", "default hot window for features")
		tables         = flag.String("tables", "market_features,market_trades,market_quotes", "comma list of table names")

		// emergency settings
		batchQuotes    = flag.Int("batch_quotes", 100000, "delete batch size for quotes in emergency")
		batchTrades    = flag.Int("batch_trades", 50000, "delete batch size for trades in emergency")
		batchFeatures  = flag.Int("batch_features", 50000, "delete batch size for features in emergency")
		emergencyGrace = flag.Duration("emergency_sleep", 5*time.Second, "sleep between emergency batches")

		// partition housekeeping
		precreateHoursBack = flag.Int("precreate_hours_back", 2, "precreate partitions hours back")
		precreateHoursFwd  = flag.Int("precreate_hours_fwd", 2, "precreate partitions hours forward")

		// How often to check for new policy
		checkInterval = flag.Duration("check_interval", 5*time.Minute, "how often to reload policy and run cleanup")
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

	// Main loop - reload config each iteration
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)

		// Load dynamic policy (includes emergency threshold)
		dynamicQuotesWindow, dynamicTradesWindow, dynamicFeaturesWindow, dynamicEmergencyMB :=
			loadDynamicPolicy(*quotesWindow, *tradesWindow, *featuresWindow)

		// Update table configs with dynamic values
		tableConfigs := map[string]*TableConfig{
			"market_features": {
				window:            dynamicFeaturesWindow,
				deleteFn:          "delete_exported_hours_features",
				dropPartitionFn:   "drop_archived_market_features_partitions_hourly",
				ensurePartitionFn: "ensure_features_partitions_hourly",
				emergencyWindow:   dynamicFeaturesWindow, // Use same as regular window
				emergencyBatch:    *batchFeatures,
				keepHours:         int(parseHours(dynamicFeaturesWindow)),
			},
			"market_trades": {
				window:            dynamicTradesWindow,
				deleteFn:          "delete_exported_hours_trades",
				dropPartitionFn:   "drop_archived_market_trades_partitions_hourly",
				ensurePartitionFn: "ensure_trades_partitions_hourly",
				emergencyWindow:   dynamicTradesWindow, // Use same as regular window
				emergencyBatch:    *batchTrades,
				keepHours:         int(parseHours(dynamicTradesWindow)),
			},
			"market_quotes": {
				window:            dynamicQuotesWindow,
				deleteFn:          "delete_exported_hours_quotes",
				dropPartitionFn:   "drop_archived_market_quotes_partitions_hourly",
				ensurePartitionFn: "ensure_quotes_partitions_hourly",
				emergencyWindow:   dynamicQuotesWindow, // Use same as regular window
				emergencyBatch:    *batchQuotes,
				keepHours:         int(parseHours(dynamicQuotesWindow)),
			},
		}

		// Run cleanup with dynamic config
		runJanitorCycle(ctx, db, tableConfigs, *tables, dynamicEmergencyMB,
			*emergencyGrace, *precreateHoursBack, *precreateHoursFwd)

		cancel()

		// Sleep before next iteration
		time.Sleep(*checkInterval)
	}
}

// Helper to parse "X.Y hours" into float
func parseHours(window string) float64 {
	var hours float64
	fmt.Sscanf(window, "%f hours", &hours)
	if hours == 0 {
		hours = 1 // Default to 1 hour minimum
	}
	return hours
}

// Extract the janitor logic into a function
func runJanitorCycle(ctx context.Context, db *sql.DB, tableConfigs map[string]*TableConfig,
	tables string, minFreeMB int, emergencyGrace time.Duration,
	precreateHoursBack, precreateHoursFwd int) {

	// Ensure partitions exist
	for tableName, config := range tableConfigs {
		if config.ensurePartitionFn != "" {
			if _, err := db.ExecContext(ctx,
				fmt.Sprintf("SELECT %s($1,$2)", config.ensurePartitionFn),
				precreateHoursBack, precreateHoursFwd,
			); err != nil {
				log.Printf("[janitor] %s failed for %s: %v", config.ensurePartitionFn, tableName, err)
			}
		}
	}

	free := freeMB("/")
	log.Printf("[janitor] free_mb=%d min_free_mb=%d", free, minFreeMB)

	// Emergency mode
	if free < minFreeMB {
		log.Printf("[janitor] EMERGENCY MODE: low disk")
		deadline := time.Now().Add(5 * time.Minute)
		round := 0

		for {
			round++
			var total int64

			emergencyOrder := []string{"market_quotes", "market_trades", "market_features"}
			for _, tableName := range emergencyOrder {
				if config, ok := tableConfigs[tableName]; ok {
					del, _ := emergencyDelete(ctx, db, tableName, config.emergencyWindow,
						config.emergencyBatch, emergencyGrace)
					total += del
				}
			}

			dbCheckpoint(ctx, db)
			free = freeMB("/")
			log.Printf("[janitor] round=%d total_deleted=%d free_mb=%d", round, total, free)

			if free >= minFreeMB || total == 0 || time.Now().After(deadline) || round >= 10 {
				break
			}
		}
	}

	// Normal mode
	for _, tableName := range splitCSV(tables) {
		config, ok := tableConfigs[tableName]
		if !ok {
			log.Printf("skip unknown table: %s", tableName)
			continue
		}

		if config.deleteFn != "" {
			callWindowed(ctx, db,
				fmt.Sprintf("SELECT %s($1)", config.deleteFn),
				config.window, tableName)
		}

		if config.dropPartitionFn != "" {
			var dropped int
			if err := db.QueryRowContext(ctx,
				fmt.Sprintf("SELECT %s($1)", config.dropPartitionFn),
				config.keepHours,
			).Scan(&dropped); err != nil {
				log.Printf("[janitor] %s failed: %v", config.dropPartitionFn, err)
			} else if dropped > 0 {
				log.Printf("[janitor] dropped %d %s partitions", dropped, tableName)
				dbCheckpoint(ctx, db)
			}
		}
	}
}

// [Rest of the helper functions remain the same...]
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func callWindowed(ctx context.Context, db *sql.DB, sqlstmt, window, table string) {
	var deleted int64
	if err := db.QueryRowContext(ctx, sqlstmt, window).Scan(&deleted); err != nil {
		log.Printf("janitor call failed (%s): %v", sqlstmt, err)
		return
	}
	log.Printf("janitor %s(%q) → deleted %d rows", sqlstmt, window, deleted)
	if deleted > 10000 {
		vacuumTable(ctx, db, table)
	}
}

func emergencyDelete(ctx context.Context, db *sql.DB, table, keep string, batch int, pause time.Duration) (int64, error) {
	log.Printf("[janitor] emergency deleting from %s older than %s", table, keep)
	var sinceVacuum, total int64

	for {
		q := fmt.Sprintf(`
WITH doomed AS (
  SELECT ctid FROM %s
  WHERE ts < now() - $1::interval
  ORDER BY ts ASC
  LIMIT %d
)
DELETE FROM %s t
USING doomed d
WHERE t.ctid = d.ctid
`, table, batch, table)

		res, err := db.ExecContext(ctx, q, keep)
		if err != nil {
			log.Printf("[janitor] emergency delete failed on %s: %v", table, err)
			return total, err
		}
		deleted, _ := res.RowsAffected()
		if deleted == 0 {
			break
		}
		total += deleted
		sinceVacuum += deleted

		log.Printf("[janitor] %s emergency batch deleted=%d (since vacuum=%d, total=%d)", table, deleted, sinceVacuum, total)

		if sinceVacuum > 10000 {
			vacuumTable(ctx, db, table)
			sinceVacuum = 0
		}
		time.Sleep(pause)
	}
	return total, nil
}

func vacuumTable(ctx context.Context, db *sql.DB, table string) {
	if table == "" {
		return
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("VACUUM (ANALYZE) %s", table)); err != nil {
		log.Printf("vacuum failed for %s: %v", table, err)
	} else {
		log.Printf("vacuumed %s successfully", table)
	}
}

func freeMB(path string) int {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int((stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024))
}

func dropEmptyFeaturePartitions(ctx context.Context, db *sql.DB, keepDays int) (int, error) {
	// [Keep the same implementation as before]
	sql := fmt.Sprintf(`
DO $$
DECLARE
  r RECORD;
  keep_before date := current_date - %d;
  nrows bigint;
  dropped int := 0;
  ymd text;
  day date;
BEGIN
  FOR r IN
    SELECT n.nspname, c.relname
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_features'
  LOOP
    ymd := regexp_replace(r.relname, '^market_features_p', '');
    BEGIN
      day := to_date(ymd, 'YYYYMMDD');
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    IF day >= keep_before THEN
      CONTINUE;
    END IF;

    EXECUTE format('SELECT count(*) FROM %%I.%%I', r.nspname, r.relname) INTO nrows;

    IF nrows = 0 THEN
      EXECUTE format('DROP TABLE %%I.%%I', r.nspname, r.relname);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RAISE NOTICE 'dropped_empty=%%', dropped;
END$$;
`, keepDays)

	if _, err := db.ExecContext(ctx, sql); err != nil {
		return 0, err
	}
	return 0, nil
}

func dbCheckpoint(ctx context.Context, db *sql.DB) {
	if _, err := db.ExecContext(ctx, "CHECKPOINT"); err != nil {
		log.Printf("[janitor] CHECKPOINT failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "SELECT pg_switch_wal()"); err != nil {
		log.Printf("[janitor] pg_switch_wal failed: %v", err)
	}
}
