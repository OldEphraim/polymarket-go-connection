-- +goose NO TRANSACTION
-- +goose Up
-- Fast-path, online indexes to speed API endpoints and “last row” lookups.

-- Latest-first scans for quotes/trades/features (O(1) MAX(ts) operations)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_quotes_ts
  ON market_quotes (ts DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_trades_ts
  ON market_trades (ts DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_features_ts
  ON market_features (ts DESC);

-- /api/stats uses a 24h window on market_events.detected_at
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_events_detected
  ON market_events (detected_at DESC);

-- +goose Down
-- Roll back only the indexes introduced in this migration.
DROP INDEX CONCURRENTLY IF EXISTS idx_market_quotes_ts;
DROP INDEX CONCURRENTLY IF EXISTS idx_market_trades_ts;
DROP INDEX CONCURRENTLY IF EXISTS idx_market_features_ts;
DROP INDEX CONCURRENTLY IF EXISTS idx_market_events_detected;