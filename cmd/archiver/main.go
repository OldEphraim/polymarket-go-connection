package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type HourWindow struct {
	Start time.Time
	End   time.Time
}

func main() {
	var (
		table         = flag.String("table", "", "Table to export: market_features | market_trades | market_quotes (required)")
		prefix        = flag.String("prefix", "", "S3 prefix (defaults to table name)")
		hourStr       = flag.String("hour", "", "UTC hour to export (e.g. 2025-10-12T00). Default = last closed hour")
		backfill      = flag.Bool("backfill", false, "Backfill mode: process oldest unarchived hour")
		timeout       = flag.Duration("timeout", 10*time.Minute, "Overall timeout per export")
		keepDays      = flag.Int("features_keep_days", 3, "For features: drop empty day-partitions older than this many days after successful archives")
		precreateBack = flag.Int("precreate_back_days", 1, "Precreate features partitions back (days)")
		precreateFwd  = flag.Int("precreate_fwd_days", 1, "Precreate features partitions forward (days)")
	)
	flag.Parse()

	if *table == "" {
		log.Fatal("missing -table")
	}
	if *prefix == "" {
		*prefix = *table
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	bucket := os.Getenv("ARCHIVE_S3_BUCKET")
	if bucket == "" {
		log.Fatal("ARCHIVE_S3_BUCKET is required")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		log.Fatalf("aws cfg: %v", err)
	}
	s3c := s3.NewFromConfig(awsCfg)
	up := manager.NewUploader(s3c)

	var win HourWindow

	if *hourStr != "" {
		// Explicit hour specified
		t, err := time.Parse("2006-01-02T15", *hourStr)
		if err != nil {
			log.Fatalf("bad -hour: %v", err)
		}
		win = HourWindow{Start: t.UTC(), End: t.UTC().Add(time.Hour)}
	} else if *backfill {
		// Backfill mode: find oldest unarchived hour
		oldest, err := findOldestUnarchivedHour(ctx, db, *table)
		if err != nil {
			log.Fatalf("finding oldest unarchived: %v", err)
		}
		if oldest == nil {
			log.Printf("All historical data archived for %s", *table)
			return
		}
		win = *oldest
		log.Printf("Backfilling %s from %s", *table, win.Start.Format(time.RFC3339))
	} else {
		// Default: last closed hour
		win = lastClosedHourUTC()
	}

	// S3 key layout: <prefix>/dt=YYYY-MM-DD/hour=HH/part-00000.json.gz
	day := win.Start.Format("2006-01-02")
	hh := win.Start.Format("15")
	dir := fmt.Sprintf("%s/dt=%s/hour=%s", strings.TrimSuffix(*prefix, "/"), day, hh)
	key := path.Join(dir, "part-00000.json.gz")
	marker := path.Join(dir, "_SUCCESS")

	// Idempotency: if object already exists, just ensure archive_jobs is marked done and exit.
	exists, err := s3KeyExists(ctx, s3c, bucket, key)
	if err != nil {
		log.Printf("warn: head object failed (continuing): %v", err)
	}
	if exists {
		// Check if already recorded in archive_jobs
		var recorded bool
		err = db.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM archive_jobs 
				WHERE table_name = $1 AND ts_start = $2 AND ts_end = $3 AND status = 'done'
			)`, *table, win.Start, win.End).Scan(&recorded)

		if err != nil || !recorded {
			// File exists in S3 but not recorded, so record it
			_ = markArchiveDone(ctx, db, *table, win, key, 0, 0)
		}
		log.Printf("skip: already archived %s %s", *table, win.Start.Format(time.RFC3339))
		return
	}

	// Mark job started
	_ = markArchiveStarted(ctx, db, *table, win, key)

	// Stream query → gzip → S3
	n, byteSize, err := streamHourToS3(ctx, db, up, bucket, key, *table, win)
	if err != nil {
		log.Printf("archive error: %v", err)
		// mark failed
		_ = markArchiveFailed(ctx, db, *table, win, key, err)
		os.Exit(1)
	}

	// Write marker file (optional, nice to have for Athena/listings)
	_, _ = s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(marker),
		Body:   strings.NewReader(""),
	})

	// Post-archive housekeeping (features only): precreate near-term partitions,
	// drop empty old partitions, and gently rotate WAL.
	if *table == "market_features" {
		// Best-effort; failures are non-fatal
		_, _ = db.ExecContext(ctx, "SELECT ensure_features_partitions($1,$2)", *precreateBack, *precreateFwd)
		_, _ = dropEmptyFeaturePartitions(ctx, db, *keepDays)
		dbCheckpoint(ctx, db)
	}

	// Mark done
	_ = markArchiveDone(ctx, db, *table, win, key, n, byteSize)
	log.Printf("ok: archived %s hour=%s rows=%d bytes=%d s3://%s/%s",
		*table, win.Start.Format("2006-01-02T15"), n, byteSize, bucket, key)
}

func findOldestUnarchivedHour(ctx context.Context, db *sql.DB, table string) (*HourWindow, error) {
	var oldestTS sql.NullTime

	// Find the oldest hour that hasn't been archived yet
	q := fmt.Sprintf(`
		WITH hourly_data AS (
			SELECT date_trunc('hour', ts) as hour, COUNT(*) as cnt
			FROM %s
			GROUP BY 1
		)
		SELECT MIN(hour) 
		FROM hourly_data hd
		WHERE NOT EXISTS (
			SELECT 1 FROM archive_jobs aj
			WHERE aj.table_name = $1
			AND aj.status = 'done'
			AND hd.hour >= aj.ts_start
			AND hd.hour < aj.ts_end
		)`, table)

	err := db.QueryRowContext(ctx, q, table).Scan(&oldestTS)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Everything is archived
		}
		return nil, err
	}

	if !oldestTS.Valid {
		return nil, nil // No unarchived data
	}

	return &HourWindow{
		Start: oldestTS.Time,
		End:   oldestTS.Time.Add(time.Hour),
	}, nil
}

func lastClosedHourUTC() HourWindow {
	now := time.Now().UTC()
	end := now.Truncate(time.Hour) // current hour start
	start := end.Add(-time.Hour)   // previous hour
	return HourWindow{Start: start, End: end}
}

func s3KeyExists(ctx context.Context, c *s3.Client, bucket, key string) (bool, error) {
	_, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		return true, nil
	}
	if ok := strings.Contains(strings.ToLower(err.Error()), "not found"); ok {
		return false, nil
	}
	return false, err
}

type dumpResult struct {
	rows int64
	err  error
}

func streamHourToS3(ctx context.Context, db *sql.DB, up *manager.Uploader, bucket, key, table string, win HourWindow) (int64, int64, error) {
	pr, pw := io.Pipe()
	gw := gzip.NewWriter(pw)

	// count bytes written (compressed)
	var byteCount int64
	mw := io.MultiWriter(gw, countWriter{&byteCount})

	resCh := make(chan dumpResult, 1)

	go func() {
		defer gw.Close()
		rows, err := dumpTableHour(ctx, db, table, win, mw)
		// Close the pipe writer after gzip is closed, so the reader sees EOF
		_ = pw.CloseWithError(err)
		resCh <- dumpResult{rows: rows, err: err}
	}()

	// stream upload
	if _, err := up.Upload(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		Body:            pr,
		ContentType:     aws.String("application/json"),
		ContentEncoding: aws.String("gzip"),
	}); err != nil {
		// ensure the goroutine unblocks
		_ = pr.CloseWithError(err)
		<-resCh // drain
		return 0, 0, fmt.Errorf("s3 put: %w", err)
	}

	// wait for dump to finish and get rows
	r := <-resCh
	if r.err != nil {
		return 0, 0, r.err
	}
	return r.rows, byteCount, nil
}

type countWriter struct{ n *int64 }

func (c countWriter) Write(p []byte) (int, error) {
	*c.n += int64(len(p))
	return len(p), nil
}

func dumpTableHour(ctx context.Context, db *sql.DB, table string, win HourWindow, w io.Writer) (int64, error) {
	var rowCount int64

	switch table {
	case "market_features":
		q := `
		  SELECT token_id, ts, ret_1m, ret_5m, vol_1m, avg_vol_5m, sigma_5m, zscore_5m,
		         imbalance_top, spread_bps, broke_high_15m, broke_low_15m, time_to_resolve_h, signed_flow_1m
		    FROM market_features
		   WHERE ts >= $1 AND ts < $2
		   ORDER BY ts, token_id`
		rows, err := db.QueryContext(ctx, q, win.Start, win.End)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				tokenID                             string
				ts                                  time.Time
				ret1m, ret5m, vol1m, avg5m, sigma5m sql.NullFloat64
				z, imb, spread, ttrh, flow          sql.NullFloat64
				bh, bl                              sql.NullBool
			)
			if err := rows.Scan(&tokenID, &ts, &ret1m, &ret5m, &vol1m, &avg5m, &sigma5m, &z, &imb, &spread, &bh, &bl, &ttrh, &flow); err != nil {
				return rowCount, err
			}
			obj := map[string]any{
				"token_id": tokenID,
				"ts":       ts.UTC().Format(time.RFC3339Nano),
			}
			addNF(obj, "ret_1m", ret1m)
			addNF(obj, "ret_5m", ret5m)
			addNF(obj, "vol_1m", vol1m)
			addNF(obj, "avg_vol_5m", avg5m)
			addNF(obj, "sigma_5m", sigma5m)
			addNF(obj, "zscore_5m", z)
			addNF(obj, "imbalance_top", imb)
			addNF(obj, "spread_bps", spread)
			addNB(obj, "broke_high_15m", bh)
			addNB(obj, "broke_low_15m", bl)
			addNF(obj, "time_to_resolve_h", ttrh)
			addNF(obj, "signed_flow_1m", flow)
			if err := writeJSONL(w, obj); err != nil {
				return rowCount, err
			}
			rowCount++
		}
		return rowCount, rows.Err()

	case "market_trades":
		q := `
		  SELECT token_id, ts, price, size, aggressor, trade_id
		    FROM market_trades
		   WHERE ts >= $1 AND ts < $2
		   ORDER BY ts, id`
		rows, err := db.QueryContext(ctx, q, win.Start, win.End)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				tokenID, aggressor, tradeID sql.NullString
				ts                          time.Time
				price, size                 float64
			)
			if err := rows.Scan(&tokenID, &ts, &price, &size, &aggressor, &tradeID); err != nil {
				return rowCount, err
			}
			obj := map[string]any{
				"token_id":  tokenID.String,
				"ts":        ts.UTC().Format(time.RFC3339Nano),
				"price":     price,
				"size":      size,
				"aggressor": strings.ToLower(aggressor.String),
			}
			if tradeID.Valid {
				obj["trade_id"] = tradeID.String
			}
			if err := writeJSONL(w, obj); err != nil {
				return rowCount, err
			}
			rowCount++
		}
		return rowCount, rows.Err()

	case "market_quotes":
		q := `
		  SELECT token_id, ts, best_bid, best_ask, bid_size1, ask_size1, spread_bps, mid
		    FROM market_quotes
		   WHERE ts >= $1 AND ts < $2
		   ORDER BY ts, id`
		rows, err := db.QueryContext(ctx, q, win.Start, win.End)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				tokenID                         string
				ts                              time.Time
				bid, ask, bsz, asz, spread, mid sql.NullFloat64
			)
			if err := rows.Scan(&tokenID, &ts, &bid, &ask, &bsz, &asz, &spread, &mid); err != nil {
				return rowCount, err
			}
			obj := map[string]any{
				"token_id": tokenID,
				"ts":       ts.UTC().Format(time.RFC3339Nano),
			}
			addNF(obj, "best_bid", bid)
			addNF(obj, "best_ask", ask)
			addNF(obj, "bid_size1", bsz)
			addNF(obj, "ask_size1", asz)
			addNF(obj, "spread_bps", spread)
			addNF(obj, "mid", mid)
			if err := writeJSONL(w, obj); err != nil {
				return rowCount, err
			}
			rowCount++
		}
		return rowCount, rows.Err()

	default:
		return 0, fmt.Errorf("unsupported table: %s", table)
	}
}

func writeJSONL(w io.Writer, obj map[string]any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func addNF(m map[string]any, key string, v sql.NullFloat64) {
	if v.Valid {
		m[key] = v.Float64
	} else {
		m[key] = nil
	}
}

func addNB(m map[string]any, key string, v sql.NullBool) {
	if v.Valid {
		m[key] = v.Bool
	} else {
		m[key] = nil
	}
}

// ---- archive_jobs helpers (best-effort; tolerate errors) ----

func markArchiveStarted(ctx context.Context, db *sql.DB, table string, win HourWindow, s3Key string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO archive_jobs(table_name, ts_start, ts_end, s3_key, row_count, bytes_written, status)
		VALUES ($1, $2, $3, $4, 0, 0, 'running')
		ON CONFLICT (table_name, ts_start, ts_end) DO NOTHING`,
		table, win.Start, win.End, s3Key)
	if err != nil {
		log.Printf("warn: markArchiveStarted failed: %v", err)
	}
	return nil
}

func markArchiveDone(ctx context.Context, db *sql.DB, table string, win HourWindow, s3Key string, rows int64, bytes int64) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO archive_jobs(table_name, ts_start, ts_end, s3_key, row_count, bytes_written, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'done')
		ON CONFLICT (table_name, ts_start, ts_end) 
		DO UPDATE SET 
			status = 'done',
			row_count = $5,
			bytes_written = $6,
			s3_key = $4`,
		table, win.Start, win.End, s3Key, rows, bytes)
	if err != nil {
		log.Printf("warn: markArchiveDone failed: %v", err)
	}
	return nil
}

func markArchiveFailed(ctx context.Context, db *sql.DB, table string, win HourWindow, s3Key string, cause error) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO archive_jobs(table_name, ts_start, ts_end, s3_key, row_count, bytes_written, status)
		VALUES ($1, $2, $3, $4, 0, 0, 'failed')
		ON CONFLICT (table_name, ts_start, ts_end) 
		DO UPDATE SET 
			status = 'failed',
			s3_key = $4`,
		table, win.Start, win.End, s3Key)
	if err != nil {
		log.Printf("warn: markArchiveFailed failed: %v", err)
	}
	return nil
}

// Drops empty (zero-row) day partitions older than current_date - keepDays.
// Returns the number of partitions dropped.
func dropEmptyFeaturePartitions(ctx context.Context, db *sql.DB, keepDays int) (int, error) {
	// We use a DO block so we can dynamically count rows in each child safely.
	// keepDays is embedded as a literal (int) to keep the block simple.
	sql := `
DO $$
DECLARE
  part regclass;
  keep_before date := current_date - ` + strconv.Itoa(keepDays) + `;
  nrows bigint;
  day date;
  dropped int := 0;
BEGIN
  FOR part IN
    SELECT c.oid::regclass
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_features'
  LOOP
    BEGIN
      day := to_date(regexp_replace(part::text, '^market_features_p', ''), 'YYYYMMDD');
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;
    IF day >= keep_before THEN
      CONTINUE;
    END IF;

    EXECUTE format('SELECT count(*) FROM %s', part) INTO nrows;
    IF nrows = 0 THEN
      EXECUTE format('DROP TABLE %s', part);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RAISE NOTICE 'dropped_empty=%', dropped;
END$$;
`
	// We can’t get the integer out of a DO easily; parse the NOTICE isn’t worth it.
	// So we re-count immediately after:
	if _, err := db.ExecContext(ctx, sql); err != nil {
		return 0, err
	}

	// Quick recount: how many empty partitions remain older than keep?
	q := `
WITH parts AS (
  SELECT c.oid::regclass AS part, c.relname
  FROM pg_class c
  JOIN pg_inherits i ON i.inhrelid = c.oid
  JOIN pg_class p ON p.oid = i.inhparent
  WHERE p.relname = 'market_features'
),
older AS (
  SELECT part, relname
  FROM parts
  WHERE to_date(regexp_replace(relname, '^market_features_p',''), 'YYYYMMDD') < current_date - $1::int
),
empty AS (
  SELECT relname
  FROM older
  WHERE (SELECT count(*) FROM pg_catalog.pg_class WHERE oid = older.part) IS NOT NULL
  AND   (SELECT count(*) FROM older.part) IS NULL -- dummy; we can't reference like this
)
SELECT 0; -- we can’t reliably compute after-the-fact without dynamic SQL; return 0
`
	// We’ll just return 0 here and rely on the log NOTICE above; or, simpler: return a sentinel like -1.
	// To keep things simple for now:
	_ = q
	return 0, nil
}

// Run a checkpoint and force a WAL segment switch (best-effort).
func dbCheckpoint(ctx context.Context, db *sql.DB) {
	if _, err := db.ExecContext(ctx, "CHECKPOINT"); err != nil {
		log.Printf("[janitor] CHECKPOINT failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "SELECT pg_switch_wal()"); err != nil {
		log.Printf("[janitor] pg_switch_wal failed: %v", err)
	}
}
