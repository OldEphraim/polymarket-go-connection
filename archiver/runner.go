package archiver

import (
	"context"
	"fmt"
	"io"
	"log"
	"path"
	"strings"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type Runner struct {
	Sink     *S3Sink
	Jobs     *JobStore
	Queries  *database.Queries
	Dumpers  map[string]Dumper
	PrefixFn func(tbl string) string
	KeepDays int // if you still want post-archive features housekeeping
}

func (r *Runner) RunOnce(ctx context.Context, table string, w Window) (bool, error) {
	prefix := r.PrefixFn(table)
	day := w.Start.Format("2006-01-02")
	hh := w.Start.Format("15")
	dir := fmt.Sprintf("%s/dt=%s/hour=%s", strings.TrimSuffix(prefix, "/"), day, hh)
	key := path.Join(dir, "part-00000.json.gz")
	marker := path.Join(dir, "_SUCCESS")

	// idempotency
	exists, err := r.Sink.Exists(ctx, key)
	if err == nil && exists {
		rec, err2 := r.Jobs.RecordedDone(ctx, table, w)
		if err2 != nil || !rec {
			_ = r.Jobs.Done(ctx, table, key, w, 0, 0)
		}
		log.Printf("[archiver][%s] skip: already archived %s", table, w.Start.Format(time.RFC3339))
		return true, nil
	} else if err != nil {
		log.Printf("[archiver][%s] warn head: %v (continuing)", table, err)
	}

	if err := r.Jobs.Start(ctx, table, key, w); err != nil {
		log.Printf("warn: mark start failed: %v", err)
	}

	d, ok := r.Dumpers[table]
	if !ok {
		return false, fmt.Errorf("unsupported table %q", table)
	}

	rows, _, err := GzipStream(ctx, d, w, func(body io.Reader) error {
		return r.Sink.Put(ctx, key, body)
	})
	if err != nil {
		_ = r.Jobs.Fail(ctx, table, key, w)
		return false, fmt.Errorf("stream put failed: %w", err)
	}

	if err := r.Jobs.Done(ctx, table, key, w, rows, 0); err != nil {
		return false, fmt.Errorf("mark done failed: %w", err)
	}
	_ = r.Sink.PutEmpty(ctx, marker)
	log.Printf("[archiver][%s] ok: hour=%s rows=%d s3://%s/%s",
		table, w.Start.Format("2006-01-02T15"), rows, r.Sink.Bucket, key)
	return true, nil
}

// Oldest, backfill, or last-closed selector
func (r *Runner) SelectWindow(ctx context.Context, table string, explicit *time.Time, backfill bool, now time.Time) (Window, bool, error) {
	switch {
	case explicit != nil:
		t := explicit.UTC().Truncate(time.Hour)
		return Window{Start: t, End: t.Add(time.Hour)}, true, nil
	case backfill:
		oldest, err := OldestUnarchived(ctx, r.Queries, table)
		if err != nil {
			return Window{}, false, err
		}
		if oldest != nil {
			t := oldest.UTC().Truncate(time.Hour)
			return Window{Start: t, End: t.Add(time.Hour)}, true, nil
		}
	}
	return LastClosedHourUTC(now), true, nil
}
