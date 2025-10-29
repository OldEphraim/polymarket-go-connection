-- +goose Up
-- Convert from daily to hourly partitioning

-- 1) Drop the daily partition functions (we'll recreate them for hourly)
DROP FUNCTION IF EXISTS drop_archived_market_features_partitions(int);
DROP FUNCTION IF EXISTS ensure_features_partitions(int, int);
DROP FUNCTION IF EXISTS create_market_features_partition(date);

-- 2) Helper: create an hourly partition with aggressive autovacuum
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_market_features_partition_hourly(p_hour timestamptz)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  part_name text := format('market_features_p%s', to_char(p_hour, 'YYYYMMDDHH24'));
  start_ts  timestamptz := date_trunc('hour', p_hour);
  end_ts    timestamptz := start_ts + interval '1 hour';
  idx_ts    text := part_name || '_ts';
  idx_tokts text := part_name || '_tok_ts';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_features
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.01,
             autovacuum_analyze_scale_factor=0.005)',
    part_name, start_ts, end_ts
  );

  -- Minimal indexes for hourly partitions
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (ts DESC)', idx_ts, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, ts DESC)', idx_tokts, part_name);
END
$func$;
-- +goose StatementEnd

-- 3) Ensure hourly partitions exist for the specified range
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_features_partitions_hourly(p_hours_back int DEFAULT 3,
                                                             p_hours_forward int DEFAULT 3)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  h timestamptz;
BEGIN
  FOR h IN
    SELECT generate_series(
      date_trunc('hour', now()) - (p_hours_back * interval '1 hour'),
      date_trunc('hour', now()) + (p_hours_forward * interval '1 hour'),
      interval '1 hour'
    )
  LOOP
    PERFORM create_market_features_partition_hourly(h);
  END LOOP;
END
$func$;
-- +goose StatementEnd

-- 4) Drop archived hourly partitions older than p_keep_hours
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drop_archived_market_features_partitions_hourly(p_keep_hours int)
RETURNS int
LANGUAGE plpgsql
AS $func$
DECLARE
  drop_before timestamptz := date_trunc('hour', now()) - (p_keep_hours * interval '1 hour');
  part RECORD;
  dropped int := 0;
  part_hour timestamptz;
BEGIN
  FOR part IN
    SELECT c.relname AS child_name,
           regexp_replace(c.relname, '^market_features_p', '') AS ymdhh
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_features'
  LOOP
    -- Parse partition hour from name
    BEGIN
      -- Handle both old daily (YYYYMMDD) and new hourly (YYYYMMDDHH) formats
      IF length(part.ymdhh) = 8 THEN
        -- Daily partition - convert to timestamp
        part_hour := to_timestamp(part.ymdhh || '00', 'YYYYMMDDHH24');
      ELSIF length(part.ymdhh) = 10 THEN
        -- Hourly partition
        part_hour := to_timestamp(part.ymdhh, 'YYYYMMDDHH24');
      ELSE
        CONTINUE;
      END IF;
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    IF part_hour >= drop_before THEN
      CONTINUE; -- within retention
    END IF;

    -- For hourly partitions, check if archived
    IF length(part.ymdhh) = 10 THEN
      -- Check if this hour is archived
      PERFORM 1
      FROM archive_jobs aj
      WHERE aj.table_name = 'market_features'
        AND aj.status = 'done'
        AND aj.ts_start = part_hour
        AND aj.ts_end = part_hour + interval '1 hour';
      
      IF FOUND THEN
        EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
        dropped := dropped + 1;
      END IF;
    ELSIF length(part.ymdhh) = 8 THEN
      -- Old daily partition - drop if older than retention
      EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RETURN dropped;
END
$func$;
-- +goose StatementEnd

-- 5) Migration helper function to handle daily to hourly transition
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION migrate_daily_to_hourly_partitions()
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  daily_part RECORD;
  row_count bigint;
  h int;
BEGIN
  -- Find daily partitions with data
  FOR daily_part IN
    SELECT c.relname
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_features'
      AND c.relname LIKE 'market_features_p________' -- 8 digits = daily
  LOOP
    EXECUTE format('SELECT COUNT(*) FROM %I', daily_part.relname) INTO row_count;
    
    IF row_count > 0 THEN
      -- Create hourly partitions for this day
      FOR h IN 0..23 LOOP
        PERFORM create_market_features_partition_hourly(
          to_timestamp(substring(daily_part.relname from 18 for 8) || lpad(h::text, 2, '0'), 'YYYYMMDDHH24')
        );
      END LOOP;
      
      -- Data will automatically route to correct hourly partition on insert
      RAISE NOTICE 'Daily partition % has % rows - create hourly partitions for migration', 
                   daily_part.relname, row_count;
    END IF;
  END LOOP;
END
$func$;
-- +goose StatementEnd

-- 6) Create initial hourly partitions and migrate
SELECT ensure_features_partitions_hourly(3, 3);
SELECT migrate_daily_to_hourly_partitions();

-- +goose Down
DROP FUNCTION IF EXISTS migrate_daily_to_hourly_partitions();
DROP FUNCTION IF EXISTS drop_archived_market_features_partitions_hourly(int);
DROP FUNCTION IF EXISTS ensure_features_partitions_hourly(int, int);
DROP FUNCTION IF EXISTS create_market_features_partition_hourly(timestamptz);