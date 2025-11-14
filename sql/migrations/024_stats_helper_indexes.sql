-- +goose Up
-- +goose NO TRANSACTION

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_events_detected_at
  ON market_events (detected_at);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_paper_trades_created_at
  ON paper_trades (created_at);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_scans_is_active
  ON market_scans (is_active);

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_market_events_detected_at;
DROP INDEX CONCURRENTLY IF EXISTS idx_paper_trades_created_at;
DROP INDEX CONCURRENTLY IF EXISTS idx_market_scans_is_active;
