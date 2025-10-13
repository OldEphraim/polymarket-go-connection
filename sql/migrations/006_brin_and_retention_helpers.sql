-- +goose NO TRANSACTION
-- +goose Up
-- BRINs for append-only time-series (CONCURRENTLY requires NO TRANSACTION)
CREATE INDEX CONCURRENTLY IF NOT EXISTS brin_market_features_ts
  ON market_features USING BRIN (ts) WITH (pages_per_range = 64);
CREATE INDEX CONCURRENTLY IF NOT EXISTS brin_market_quotes_ts
  ON market_quotes USING BRIN (ts) WITH (pages_per_range = 64);
CREATE INDEX CONCURRENTLY IF NOT EXISTS brin_market_trades_ts
  ON market_trades USING BRIN (ts) WITH (pages_per_range = 64);
CREATE INDEX CONCURRENTLY IF NOT EXISTS brin_market_events_detected
  ON market_events USING BRIN (detected_at) WITH (pages_per_range = 64);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_exported_hours_features(p_window TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  cutoff TIMESTAMPTZ;
  n BIGINT;
BEGIN
  cutoff := NOW() - (p_window::interval);

  WITH gone AS (
    DELETE FROM market_features mf
    USING archive_jobs aj
    WHERE aj.table_name = 'market_features'
      AND aj.status = 'done'
      AND aj.ts_start < cutoff
      AND mf.ts >= aj.ts_start
      AND mf.ts <  aj.ts_end
    RETURNING 1
  )
  SELECT COUNT(*) INTO n FROM gone;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_features','deleted',n,'cutoff',cutoff)::text
  );

  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS delete_exported_hours_features(TEXT);
DROP INDEX CONCURRENTLY IF EXISTS brin_market_events_detected;
DROP INDEX CONCURRENTLY IF EXISTS brin_market_trades_ts;
DROP INDEX CONCURRENTLY IF EXISTS brin_market_quotes_ts;
DROP INDEX CONCURRENTLY IF EXISTS brin_market_features_ts;