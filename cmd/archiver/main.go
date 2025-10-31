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
	"os/signal"
	"path"
	"strings"
	"syscall"
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

type RetentionPolicy struct {
	Timestamp          time.Time `json:"timestamp"`
	FeaturesHours      float64   `json:"features_hours"`
	TradesHours        float64   `json:"trades_hours"`
	QuotesHours        float64   `json:"quotes_hours"`
	ArchiveSleepSecs   int       `json:"archive_sleep_secs"`
	JanitorSleepSecs   int       `json:"janitor_sleep_secs"`
	EmergencyThreshold int       `json:"emergency_threshold_mb"`

	// Optional fields (ignored if not present in the JSON)
	BackpressureFreeMB int  `json:"backpressure_free_mb,omitempty"`
	PauseGatherer      bool `json:"pause_gatherer,omitempty"`
}

const (
	defaultPolicyPath    = "/tmp/retention_policy.json"
	defaultArchiveSleep  = 60 * time.Second
	minArchiveSleep      = 5 * time.Second
	maxArchiveSleep      = 5 * time.Minute
	maxBackoffSleep      = 2 * time.Minute
	noWorkSleepFloor     = 5 * time.Second
	shortSuccessPause    = 1 * time.Second
	policyReloadInterval = 15 * time.Second
	// Fallback if BackpressureFreeMB is not present in the policy file
	defaultBackpressureFreeMB = 256
)

func main() {
	// ---- Flags ----
	var (
		// New multi-table flags
		tablesCSV   = flag.String("tables", "", "Comma-separated list of tables to export (e.g. market_features,market_trades,market_quotes)")
		concurrency = flag.Int("concurrency", 0, "Max concurrent hour-archives across all tables (default: number of tables)")

		// Legacy/compat flags (still supported)
		table    = flag.String("table", "", "Single table to export (deprecated if -tables is set)")
		prefix   = flag.String("prefix", "", "S3 prefix (defaults to table name)")
		hourStr  = flag.String("hour", "", "UTC hour to export (e.g. 2025-10-12T00); if set, run once per table then exit")
		backfill = flag.Bool("backfill", false, "Backfill oldest unarchived hour(s) before catching up")
		timeout  = flag.Duration("timeout", 10*time.Minute, "Overall timeout per export unit (one hour)")
		keepDays = flag.Int("features_keep_days", 3, "For features: drop empty day partitions older than this many days after successful archives")
	)
	flag.Parse()

	// Resolve table list
	tables := parseTables(*tablesCSV)
	if len(tables) == 0 {
		if *table == "" {
			log.Fatal("missing -tables (preferred) or -table")
		}
		tables = []string{*table}
	}
	if *concurrency <= 0 || *concurrency > len(tables) {
		*concurrency = len(tables)
	}

	// Resolve S3 prefix behavior:
	// - If -prefix given, use it for ALL tables.
	// - Else default per-table prefix == that table's name.
	resolvePrefix := func(tbl string) string {
		if *prefix != "" {
			return *prefix
		}
		return tbl
	}

	// Env
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

	// Long-lived clients
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	awsCfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(region))
	if err != nil {
		log.Fatalf("aws cfg: %v", err)
	}
	s3c := s3.NewFromConfig(awsCfg)
	up := manager.NewUploader(s3c)

	// One-shot mode: run the requested hour for ALL tables and exit.
	if *hourStr != "" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()

		var hadWork bool
		var firstErr error
		for _, tbl := range tables {
			pfx := resolvePrefix(tbl)
			did, err := runOnce(ctx, db, s3c, up, tbl, pfx, *hourStr, *backfill, bucket, *keepDays)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			hadWork = hadWork || did
		}
		if firstErr != nil {
			log.Fatalf("one-shot archiving encountered error: %v", firstErr)
		}
		if !hadWork {
			log.Printf("no work for hour=%s (already archived or empty)", *hourStr)
		}
		return
	}

	// Graceful shutdown
	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Semaphore to cap concurrent runOnce calls across all workers
	sem := make(chan struct{}, *concurrency)

	// Spawn a worker per table
	errCh := make(chan error, len(tables))
	for _, tbl := range tables {
		tbl := tbl
		pfx := resolvePrefix(tbl)
		go func() {
			errCh <- workerLoop(rootCtx, db, s3c, up, tbl, pfx, bucket, *keepDays, *timeout, sem)
		}()
	}

	// Wait for context cancel OR a worker returning a fatal error
	select {
	case <-rootCtx.Done():
		log.Printf("[archiver] shutting down (signal)")
	case err := <-errCh:
		if err != nil && rootCtx.Err() == nil {
			log.Printf("[archiver] worker fatal error: %v", err)
			cancel()
		}
		// Drain other errors (non-blocking)
	Drain:
		for {
			select {
			case e := <-errCh:
				if e != nil {
					log.Printf("[archiver] worker error: %v", e)
				}
			default:
				break Drain
			}
		}
	}
}

// workerLoop: one goroutine per table; respects dynamic pacing and a global concurrency cap (sem).
func workerLoop(
	root context.Context,
	db *sql.DB,
	s3c *s3.Client,
	up *manager.Uploader,
	table string,
	prefix string,
	bucket string,
	keepDays int,
	timeout time.Duration,
	sem chan struct{},
) error {
	var (
		backoff       = 1.0
		lastPolicy    RetentionPolicy
		lastLoad      time.Time
		backpressureM = defaultBackpressureFreeMB
	)

	for {
		if err := root.Err(); err != nil {
			return nil // shutting down
		}

		// Reload policy periodically (best-effort)
		if time.Since(lastLoad) > policyReloadInterval {
			if p, err := loadPolicy(defaultPolicyPath); err == nil {
				lastPolicy = p
				if p.BackpressureFreeMB > 0 {
					backpressureM = p.BackpressureFreeMB
				}
			}
			lastLoad = time.Now()
		}
		sleepBase := policyToSleep(lastPolicy)

		// Acquire global concurrency token
		select {
		case sem <- struct{}{}:
		case <-root.Done():
			return nil
		}

		// Run exactly one hour unit for this table
		ctx, cancel := context.WithTimeout(root, timeout)
		didWork, err := runOnce(ctx, db, s3c, up, table, prefix, "", shouldBackfill(lastPolicy), bucket, keepDays)
		cancel()

		// Release token
		select {
		case <-sem:
		default:
			// should never happen; keep program robust
		}

		switch {
		case err != nil:
			// Backoff on error; clamp; extend if disk is tight
			backoff = minf(backoff*2, 16)
			sleep := time.Duration(backoff) * sleepBase
			if sleep > maxBackoffSleep {
				sleep = maxBackoffSleep
			}
			if freeMB("/") < backpressureM {
				if sleep < maxArchiveSleep/2 {
					sleep = maxArchiveSleep / 2
				}
			}
			log.Printf("[archiver][%s] error: %v; backoff %s", table, err, sleep)
			if err := sleepOrDone(root, sleep); err != nil {
				return nil
			}

		case didWork:
			// Small pause to let other tables move, then go again
			backoff = 1.0
			if err := sleepOrDone(root, shortSuccessPause); err != nil {
				return nil
			}

		default:
			// Idle: nothing to do (hour not closed or backfill caught up)
			backoff = 1.0
			sleep := sleepBase
			if sleep < noWorkSleepFloor {
				sleep = noWorkSleepFloor
			}
			log.Printf("[archiver][%s] idle: sleeping %s", table, sleep)
			if err := sleepOrDone(root, sleep); err != nil {
				return nil
			}
		}
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func shouldBackfill(p RetentionPolicy) bool {
	// In Option A we keep honoring the process-level -backfill by simply
	// defaulting to “true” behavior when there’s space pressure.
	// If you prefer strict CLI semantics only, hardcode to ‘false’ and use flags per container.
	// For now, we just always allow backfill-attempt (it will no-op once caught up).
	return true
}

// runOnce does a single “hour” of work for one table.
// - If hourStr is non-empty, uses that exact hour.
// - Else if backfill, finds the oldest unarchived hour.
// - Else uses last closed hour.
// Returns didWork=true if it actually archived something (or recorded an existing S3 object).
func runOnce(
	parent context.Context,
	db *sql.DB,
	s3c *s3.Client,
	up *manager.Uploader,
	table string,
	prefix string,
	hourStr string,
	backfill bool,
	bucket string,
	keepDays int,
) (bool, error) {

	// Decide the window
	var win HourWindow
	switch {
	case hourStr != "":
		t, err := time.Parse("2006-01-02T15", hourStr)
		if err != nil {
			return false, fmt.Errorf("bad -hour: %w", err)
		}
		win = HourWindow{Start: t.UTC(), End: t.UTC().Add(time.Hour)}

	case backfill:
		oldest, err := findOldestUnarchivedHour(parent, db, table)
		if err != nil {
			return false, fmt.Errorf("finding oldest unarchived: %w", err)
		}
		if oldest == nil {
			// Nothing left to backfill; fall through to last-closed hour
			win = lastClosedHourUTC()
		} else {
			win = *oldest
			log.Printf("[archiver][%s] backfilling from %s", table, win.Start.Format(time.RFC3339))
		}

	default:
		win = lastClosedHourUTC()
	}

	// S3 key layout: <prefix>/dt=YYYY-MM-DD/hour=HH/part-00000.json.gz
	day := win.Start.Format("2006-01-02")
	hh := win.Start.Format("15")
	dir := fmt.Sprintf("%s/dt=%s/hour=%s", strings.TrimSuffix(prefix, "/"), day, hh)
	key := path.Join(dir, "part-00000.json.gz")
	marker := path.Join(dir, "_SUCCESS")

	// Idempotency: if object already exists, ensure archive_jobs is marked done and exit.
	exists, err := s3KeyExists(parent, s3c, bucket, key)
	if err != nil {
		// Non-fatal: continue so we still might archive (e.g., transient S3 HEAD issues)
		log.Printf("[archiver][%s] warn: head object failed (continuing): %v", table, err)
	}
	if exists {
		var recorded bool
		err = db.QueryRowContext(parent, `
			SELECT EXISTS(
				SELECT 1 FROM archive_jobs 
				WHERE table_name = $1 AND ts_start = $2 AND ts_end = $3 AND status = 'done'
			)`, table, win.Start, win.End).Scan(&recorded)

		if err != nil || !recorded {
			_ = markArchiveDone(parent, db, table, win, key, 0, 0)
		}
		log.Printf("[archiver][%s] skip: already archived %s", table, win.Start.Format(time.RFC3339))
		return true, nil
	}

	// Mark job started (best-effort)
	_ = markArchiveStarted(parent, db, table, win, key)

	// Stream query → gzip → S3
	n, byteSize, err := streamHourToS3(parent, db, up, bucket, key, table, win)
	if err != nil {
		_ = markArchiveFailed(parent, db, table, win, key, err)
		return false, fmt.Errorf("archive stream error: %w", err)
	}

	// Write marker (best-effort)
	_, _ = s3c.PutObject(parent, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(marker),
		Body:   strings.NewReader(""),
	})

	// Post-archive housekeeping (features only)
	if table == "market_features" {
		_, _ = db.ExecContext(parent, "SELECT ensure_features_partitions_hourly($1,$2)", 2, 2)
		_, _ = dropEmptyFeaturePartitions(parent, db, keepDays)
		dbCheckpoint(parent, db)
	}

	// Mark done
	_ = markArchiveDone(parent, db, table, win, key, n, byteSize)
	log.Printf("[archiver][%s] ok: hour=%s rows=%d bytes=%d s3://%s/%s",
		table, win.Start.Format("2006-01-02T15"), n, byteSize, bucket, key)
	return true, nil
}

func findOldestUnarchivedHour(ctx context.Context, db *sql.DB, table string) (*HourWindow, error) {
	var oldestTS sql.NullTime
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

	if err := db.QueryRowContext(ctx, q, table).Scan(&oldestTS); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !oldestTS.Valid {
		return nil, nil
	}
	return &HourWindow{Start: oldestTS.Time, End: oldestTS.Time.Add(time.Hour)}, nil
}

func lastClosedHourUTC() HourWindow {
	now := time.Now().UTC()
	end := now.Truncate(time.Hour)
	start := end.Add(-time.Hour)
	return HourWindow{Start: start, End: end}
}

func s3KeyExists(ctx context.Context, c *s3.Client, bucket, key string) (bool, error) {
	_, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err == nil {
		return true, nil
	}
	// SDK v2 may not always type NotFound; do a safe string-based check
	lo := strings.ToLower(err.Error())
	if strings.Contains(lo, "not found") || strings.Contains(lo, "status code: 404") {
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

	var byteCount int64
	mw := io.MultiWriter(gw, countWriter{&byteCount})
	resCh := make(chan dumpResult, 1)

	go func() {
		defer gw.Close()
		rows, err := dumpTableHour(ctx, db, table, win, mw)
		_ = pw.CloseWithError(err)
		resCh <- dumpResult{rows: rows, err: err}
	}()

	if _, err := up.Upload(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		Body:            pr,
		ContentType:     aws.String("application/json"),
		ContentEncoding: aws.String("gzip"),
	}); err != nil {
		_ = pr.CloseWithError(err)
		<-resCh
		return 0, 0, fmt.Errorf("s3 put: %w", err)
	}

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

// Drops empty (zero-row) day partitions older than current_date - keepDays (best-effort)
func dropEmptyFeaturePartitions(ctx context.Context, db *sql.DB, keepDays int) (int, error) {
	sqlStmt := fmt.Sprintf(`
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

	if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
		return 0, err
	}
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

// ---- dynamic pacing helpers ----

func loadPolicy(path string) (RetentionPolicy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return RetentionPolicy{}, err
	}
	var p RetentionPolicy
	if err := json.Unmarshal(b, &p); err != nil {
		return RetentionPolicy{}, err
	}
	return p, nil
}

func policyToSleep(p RetentionPolicy) time.Duration {
	secs := p.ArchiveSleepSecs
	if secs <= 0 {
		return defaultArchiveSleep
	}
	d := time.Duration(secs) * time.Second
	if d < minArchiveSleep {
		return minArchiveSleep
	}
	if d > maxArchiveSleep {
		return maxArchiveSleep
	}
	return d
}

func freeMB(path string) int {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int((stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024))
}

func parseTables(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
