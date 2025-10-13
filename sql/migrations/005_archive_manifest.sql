-- +goose Up
CREATE TABLE IF NOT EXISTS archive_jobs (
  id BIGSERIAL PRIMARY KEY,
  table_name TEXT NOT NULL,         -- 'market_features', 'market_trades', 'market_quotes'
  ts_start  TIMESTAMPTZ NOT NULL,   -- inclusive
  ts_end    TIMESTAMPTZ NOT NULL,   -- exclusive
  s3_key    TEXT NOT NULL,          -- where file was written
  row_count BIGINT NOT NULL,
  bytes_written BIGINT NOT NULL,
  status    TEXT NOT NULL DEFAULT 'done', -- 'done' | 'failed'
  created_at TIMESTAMPTZ DEFAULT now(),
  CONSTRAINT archive_jobs_time_ok CHECK (ts_end > ts_start),
  UNIQUE (table_name, ts_start, ts_end)
);

-- Helper partial index used by janitor delete functions
CREATE INDEX IF NOT EXISTS idx_archive_jobs_table_ts_done
  ON archive_jobs (table_name, ts_start)
  WHERE status = 'done';

-- +goose Down
DROP INDEX IF EXISTS idx_archive_jobs_table_ts_done;
DROP TABLE IF EXISTS archive_jobs;