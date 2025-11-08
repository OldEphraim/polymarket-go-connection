-- 020_temp_features_table.sql

-- +goose Up
-- Create a function that makes a uniquely named temp table (LIKE the stage schema)
-- and returns its qualified name (e.g., 'pg_temp.mfs_stage_20251108_083012_123456789').
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_unique_temp_market_features_stage()
RETURNS text
LANGUAGE plpgsql
AS $func$
DECLARE
  tname text := 'mfs_stage_' ||
                to_char(clock_timestamp(), 'YYYYMMDD_HH24MISS') || '_' ||
                lpad(floor(random()*1e9)::bigint::text, 9, '0');
BEGIN
  -- Create a temp table cloned from public.market_features_stage.
  EXECUTE format(
    'CREATE TEMP TABLE %I (LIKE public.market_features_stage INCLUDING ALL) ON COMMIT DROP',
    tname
  );
  -- Return the session-local qualified name
  RETURN 'pg_temp.' || tname;
END
$func$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS create_unique_temp_market_features_stage();
-- +goose StatementEnd
