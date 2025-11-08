package archiver

import (
	"context"
	"database/sql"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type JobStore struct{ Q *database.Queries }

func (s *JobStore) Start(ctx context.Context, table, key string, w Window) error {
	return s.Q.ArchiveStart(ctx, database.ArchiveStartParams{
		TableName: table,
		TsStart:   sql.NullTime{Time: w.Start, Valid: true},
		TsEnd:     sql.NullTime{Time: w.End, Valid: true},
		S3Key:     key,
	})
}
func (s *JobStore) Done(ctx context.Context, table, key string, w Window, rows, bytes int64) error {
	return s.Q.ArchiveDone(ctx, database.ArchiveDoneParams{
		TableName:    table,
		TsStart:      sql.NullTime{Time: w.Start, Valid: true},
		TsEnd:        sql.NullTime{Time: w.End, Valid: true},
		S3Key:        key,
		RowCount:     rows,
		BytesWritten: bytes,
	})
}
func (s *JobStore) Fail(ctx context.Context, table, key string, w Window) error {
	return s.Q.ArchiveFail(ctx, database.ArchiveFailParams{
		TableName: table,
		TsStart:   sql.NullTime{Time: w.Start, Valid: true},
		TsEnd:     sql.NullTime{Time: w.End, Valid: true},
		S3Key:     key,
	})
}
func (s *JobStore) RecordedDone(ctx context.Context, table string, w Window) (bool, error) {
	return s.Q.ArchiveRecordedDone(ctx, database.ArchiveRecordedDoneParams{
		TableName: table,
		TsStart:   sql.NullTime{Time: w.Start, Valid: true},
		TsEnd:     sql.NullTime{Time: w.End, Valid: true},
	})
}

// Oldest per-table (typed; no string interpolation)
func OldestUnarchived(ctx context.Context, q *database.Queries, table string) (*time.Time, error) {
	var nt sql.NullTime
	var err error
	switch table {
	case "market_features":
		nt, err = q.OldestUnarchivedFeaturesHour(ctx)
	case "market_trades":
		nt, err = q.OldestUnarchivedTradesHour(ctx)
	case "market_quotes":
		nt, err = q.OldestUnarchivedQuotesHour(ctx)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !nt.Valid {
		return nil, nil
	}
	return &nt.Time, nil
}
