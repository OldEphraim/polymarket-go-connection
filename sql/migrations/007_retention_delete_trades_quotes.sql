-- +goose Up
-- Guard index (created in 005; harmless if it already exists)
CREATE INDEX IF NOT EXISTS idx_archive_jobs_table_ts_done
  ON archive_jobs (table_name, ts_start)
  WHERE status = 'done';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_exported_hours_trades(p_window TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  cutoff TIMESTAMPTZ;
  n BIGINT;
BEGIN
  cutoff := NOW() - (p_window::interval);

  WITH gone AS (
    DELETE FROM market_trades t
    USING archive_jobs aj
    WHERE aj.table_name = 'market_trades'
      AND aj.status = 'done'
      AND aj.ts_start < cutoff
      AND t.ts >= aj.ts_start
      AND t.ts <  aj.ts_end
    RETURNING 1
  )
  SELECT COUNT(*) INTO n FROM gone;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_trades','deleted',n,'cutoff',cutoff)::text
  );

  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_exported_hours_quotes(p_window TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  cutoff TIMESTAMPTZ;
  n BIGINT;
BEGIN
  cutoff := NOW() - (p_window::interval);

  WITH gone AS (
    DELETE FROM market_quotes q
    USING archive_jobs aj
    WHERE aj.table_name = 'market_quotes'
      AND aj.status = 'done'
      AND aj.ts_start < cutoff
      AND q.ts >= aj.ts_start
      AND q.ts <  aj.ts_end
    RETURNING 1
  )
  SELECT COUNT(*) INTO n FROM gone;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_quotes','deleted',n,'cutoff',cutoff)::text
  );

  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS delete_exported_hours_quotes(TEXT);
DROP FUNCTION IF EXISTS delete_exported_hours_trades(TEXT);