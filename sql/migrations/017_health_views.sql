-- +goose Up
-- Catalog helpers for healthmonitor/sqlc to introspect hourly partitions safely.

-- poly_partition_span(pattern text)
-- Returns (count, oldest_ts, newest_ts) for partition tables whose names match `pattern`.
-- Example pattern: 'market_%_p2025%'. We purposely go through pg_catalog in a STABLE SQL fn.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION poly_partition_span(pattern text)
RETURNS TABLE (
  partition_count int,
  oldest_partition timestamptz,
  newest_partition timestamptz
)
LANGUAGE sql
STABLE
AS $func$
  WITH partitions AS (
    SELECT
      t.schemaname,
      t.tablename,
      CASE
        -- Strip the 'market_<something>_p' prefix; expect exactly 10 digits YYYYMMDDHH
        WHEN length(regexp_replace(t.tablename, '^market_[[:alnum:]_]+_p', '')) = 10
        THEN to_timestamp(regexp_replace(t.tablename, '^market_[[:alnum:]_]+_p', ''), 'YYYYMMDDHH24')
        ELSE NULL
      END AS partition_time
    FROM pg_catalog.pg_tables AS t
    WHERE t.tablename LIKE pattern
  )
  SELECT
    COUNT(*)::int,
    MIN(partition_time),
    MAX(partition_time)
  FROM partitions
  WHERE partition_time IS NOT NULL;
$func$;
-- +goose StatementEnd

-- poly_sum_partition_sizes_mb(pattern text)
-- Sums total relation size for all matching tables (schema-qualified) and returns MB as float.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION poly_sum_partition_sizes_mb(pattern text)
RETURNS double precision
LANGUAGE sql
STABLE
AS $func$
  SELECT COALESCE(
    SUM(pg_total_relation_size(format('%I.%I', t.schemaname, t.tablename)))::float / 1024 / 1024,
    0
  )
  FROM pg_catalog.pg_tables AS t
  WHERE t.tablename LIKE pattern;
$func$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS poly_sum_partition_sizes_mb(text);
DROP FUNCTION IF EXISTS poly_partition_span(text);
-- +goose StatementEnd
