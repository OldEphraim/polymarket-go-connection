package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	var (
		window         = flag.String("window", "6 hours", "hot window for quotes when not in emergency")
		tradesWindow   = flag.String("trades_window", "24 hours", "hot window for trades")
		featuresWindow = flag.String("features_window", "7 days", "hot window for features")
		tables         = flag.String("tables", "features,trades,quotes", "comma list: features,trades,quotes")

		minFreeMB      = flag.Int("min_free_mb", 1500, "emergency threshold (MB) for free space")
		batchQuotes    = flag.Int("batch_quotes", 100000, "delete batch size for quotes in emergency")
		batchTrades    = flag.Int("batch_trades", 50000, "delete batch size for trades in emergency")
		batchFeatures  = flag.Int("batch_features", 50000, "delete batch size for features in emergency")
		emergencyGrace = flag.Duration("emergency_sleep", 5*time.Second, "sleep between emergency batches")
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	free := freeMB("/")
	log.Printf("[janitor] free_mb=%d min_free_mb=%d", free, *minFreeMB)

	// Emergency mode: ignore archive markers; delete by ts in chunks until we’re above water
	if free < *minFreeMB {
		log.Printf("[janitor] EMERGENCY MODE: low disk")
		emergencyDelete(ctx, db, "market_quotes", "6 hours", *batchQuotes, *emergencyGrace)  // quotes are cheapest to drop
		emergencyDelete(ctx, db, "market_trades", "24 hours", *batchTrades, *emergencyGrace) // then trades
		emergencyDelete(ctx, db, "market_features", "7 days", *batchFeatures, *emergencyGrace)
		return
	}

	// Normal mode: respect archive markers
	for _, t := range splitCSV(*tables) {
		switch t {
		case "features":
			callWindowed(ctx, db, "SELECT delete_exported_hours_features($1)", *featuresWindow, "market_features")
		case "trades":
			callWindowed(ctx, db, "SELECT delete_exported_hours_trades($1)", *tradesWindow, "market_trades")
		case "quotes":
			callWindowed(ctx, db, "SELECT delete_exported_hours_quotes($1)", *window, "market_quotes")
		default:
			log.Printf("skip unknown table: %s", t)
		}
	}
}

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

func emergencyDelete(ctx context.Context, db *sql.DB, table, keep string, batch int, pause time.Duration) {
	log.Printf("[janitor] emergency deleting from %s older than %s", table, keep)
	var sinceVacuum int64

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
			break
		}
		deleted, _ := res.RowsAffected()
		if deleted == 0 {
			break
		}

		sinceVacuum += deleted
		log.Printf("[janitor] %s emergency batch deleted=%d (since vacuum=%d)", table, deleted, sinceVacuum)

		if sinceVacuum > 10000 {
			vacuumTable(ctx, db, table)
			sinceVacuum = 0
		}
		time.Sleep(pause)
	}
}

func vacuumTable(ctx context.Context, db *sql.DB, table string) {
	if table == "" {
		return
	}
	_, err := db.ExecContext(ctx, fmt.Sprintf("VACUUM (ANALYZE) %s", table))
	if err != nil {
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
	// available blocks * size / 1024 / 1024
	return int((stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024))
}
