package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"net/http"
	_ "net/http/pprof"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "github.com/lib/pq"

	"github.com/OldEphraim/polymarket-go-connection/archiver"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

func main() {
	if addr := os.Getenv("ARCHIVER_DEBUG_ADDR"); addr != "" {
		go func() {
			log.Printf("archiver pprof listening on %s", addr)
			_ = http.ListenAndServe(addr, nil)
		}()
	}

	var (
		tablesCSV   = flag.String("tables", "", "Comma-separated tables: market_features,market_trades,market_quotes")
		table       = flag.String("table", "", "Single table (deprecated if -tables)")
		prefix      = flag.String("prefix", "", "S3 prefix (defaults to table name)")
		hourStr     = flag.String("hour", "", "UTC hour (e.g. 2025-10-12T00) for one-shot")
		backfill    = flag.Bool("backfill", false, "Backfill oldest unarchived hour(s)")
		timeout     = flag.Duration("timeout", 10*time.Minute, "Timeout per hour unit")
		concurrency = flag.Int("concurrency", 0, "Max concurrent hour-archives across all tables")
	)
	flag.Parse()

	tables := parseTables(*tablesCSV, *table)
	valid := map[string]bool{"market_features": true, "market_trades": true, "market_quotes": true}
	for _, t := range tables {
		if !valid[t] {
			log.Fatalf("unsupported table %q", t)
		}
	}
	if *concurrency <= 0 || *concurrency > len(tables) {
		*concurrency = len(tables)
	}

	dsn := mustEnv("DATABASE_URL")
	bucket := mustEnv("ARCHIVE_S3_BUCKET")
	region := getenvDefault("AWS_REGION", "us-east-1")

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	q := database.New(db)

	awsCfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(region))
	if err != nil {
		log.Fatalf("aws cfg: %v", err)
	}
	s3c := s3.NewFromConfig(awsCfg)
	up := manager.NewUploader(s3c)

	r := &archiver.Runner{
		Sink:    &archiver.S3Sink{Bucket: bucket, Client: s3c, Up: up},
		Jobs:    &archiver.JobStore{Q: q},
		Queries: q,
		Dumpers: map[string]archiver.Dumper{
			"market_features": &archiver.FeaturesDumper{Q: q},
			"market_trades":   &archiver.TradesDumper{Q: q},
			"market_quotes":   &archiver.QuotesDumper{Q: q},
		},
		PrefixFn: func(tbl string) string {
			if *prefix != "" {
				return *prefix
			}
			return tbl
		},
	}

	var explicit *time.Time
	if *hourStr != "" {
		t, err := time.Parse("2006-01-02T15", *hourStr)
		if err != nil {
			log.Fatalf("bad -hour: %v", err)
		}
		explicit = &t
	}

	// One-shot?
	if explicit != nil {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		var firstErr error
		var hadWork bool
		for _, tbl := range tables {
			w, _, err := r.SelectWindow(ctx, tbl, explicit, *backfill, time.Now())
			if err == nil {
				did, e := r.RunOnce(ctx, tbl, w)
				hadWork = hadWork || did
				if e != nil && firstErr == nil {
					firstErr = e
				}
			} else if firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			log.Fatalf("errors: %v", firstErr)
		}
		if !hadWork {
			log.Printf("no work for hour=%s", *hourStr)
		}
		return
	}

	// Continuous
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	sem := make(chan struct{}, *concurrency)
	errCh := make(chan error, len(tables))

	for _, tbl := range tables {
		tbl := tbl
		go func() {
			errCh <- worker(rootCtx, sem, *timeout, func(ctx context.Context) error {
				w, _, err := r.SelectWindow(ctx, tbl, nil, true, time.Now())
				if err != nil {
					return err
				}
				_, e := r.RunOnce(ctx, tbl, w)
				return e
			})
		}()
	}

	select {
	case <-rootCtx.Done():
		log.Printf("[archiver] shutting down")
	case err := <-errCh:
		if err != nil && rootCtx.Err() == nil {
			log.Printf("[archiver] worker error: %v", err)
			stop()
		}
	}
}

func worker(root context.Context, sem chan struct{}, timeout time.Duration, fn func(ctx context.Context) error) error {
	for {
		if root.Err() != nil {
			return nil
		}
		select {
		case sem <- struct{}{}:
		case <-root.Done():
			return nil
		}
		ctx, cancel := context.WithTimeout(root, timeout)
		err := fn(ctx)
		cancel()
		select {
		case <-sem:
		default:
		}
		sleep := 1 * time.Second
		if err != nil {
			log.Printf("[archiver] error: %v", err)
			sleep = 10 * time.Second
		}
		t := time.NewTimer(sleep)
		select {
		case <-root.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

func parseTables(csv, single string) []string {
	if csv == "" && single == "" {
		log.Fatal("missing -tables or -table")
	}
	if csv != "" {
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
	return []string{single}
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}
func getenvDefault(k, d string) string {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	return v
}
