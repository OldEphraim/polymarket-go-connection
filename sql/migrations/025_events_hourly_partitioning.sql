-- +goose Up
-- Convert market_events to hourly partitioning
-- NOTE: Unlike quotes/trades/features, we do NOT archive events to S3 because
-- they can be reconstructed from archived features data. We just drop old partitions.

-- 1) Move existing table aside
ALTER TABLE market_events RENAME TO market_events_old;

-- 2) Drop the sequence ownership temporarily
ALTER SEQUENCE market_events_id_seq OWNED BY NONE;

-- 3) Create new partitioned table
CREATE TABLE market_events (
    id          integer NOT NULL DEFAULT nextval('market_events_id_seq'::regclass),
    token_id    varchar(80) NOT NULL,
    event_type  varchar(50),
    old_value   double precision,
    new_value   double precision,
    metadata    jsonb,
    detected_at timestamptz DEFAULT now(),
    PRIMARY KEY (id, detected_at)
) PARTITION BY RANGE (detected_at);

-- 4) Reassign sequence ownership
ALTER SEQUENCE market_events_id_seq OWNED BY market_events.id;

-- 5) Helper function for hourly partitions
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_market_events_partition_hourly(p_hour timestamptz)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  part_name text := format('market_events_p%s', to_char(p_hour, 'YYYYMMDDHH24'));
  start_ts  timestamptz := date_trunc('hour', p_hour);
  end_ts    timestamptz := start_ts + interval '1 hour';
  idx_detected text := part_name || '_detected';
  idx_tokdet   text := part_name || '_tok_det';
  idx_type     text := part_name || '_type';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_events
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.02,
             autovacuum_analyze_scale_factor=0.01)',
    part_name, start_ts, end_ts
  );

  -- Indexes matching the original table
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (detected_at DESC)', idx_detected, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, detected_at DESC)', idx_tokdet, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (event_type, detected_at DESC)', idx_type, part_name);
END
$func$;
-- +goose StatementEnd

-- 6) Ensure hourly partitions function
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_events_partitions_hourly(p_hours_back int DEFAULT 3,
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
    PERFORM create_market_events_partition_hourly(h);
  END LOOP;
END
$func$;
-- +goose StatementEnd

-- 7) Drop old partitions function
-- NOTE: Unlike other tables, we do NOT check archive_jobs since events are reconstructable.
-- We simply drop partitions older than p_keep_hours.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drop_old_market_events_partitions_hourly(p_keep_hours int)
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
           regexp_replace(c.relname, '^market_events_p', '') AS ymdhh
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_events'
  LOOP
    BEGIN
      IF length(part.ymdhh) = 10 THEN
        part_hour := to_timestamp(part.ymdhh, 'YYYYMMDDHH24');
      ELSE
        CONTINUE;
      END IF;
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    IF part_hour >= drop_before THEN
      CONTINUE;
    END IF;

    -- No archive check needed - just drop old partitions
    EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
    dropped := dropped + 1;
  END LOOP;

  RETURN dropped;
END
$func$;
-- +goose StatementEnd

-- 8) Create initial partitions
SELECT ensure_events_partitions_hourly(3, 3);

-- 9) Create view for old data (can be dropped after confirming migration)
CREATE OR REPLACE VIEW market_events_old_view AS
  SELECT * FROM market_events_old;

-- +goose Down
DROP VIEW IF EXISTS market_events_old_view;
DROP FUNCTION IF EXISTS drop_old_market_events_partitions_hourly(int);
DROP FUNCTION IF EXISTS ensure_events_partitions_hourly(int, int);
DROP FUNCTION IF EXISTS create_market_events_partition_hourly(timestamptz);

-- Drop child partitions and restore old table
DO $$
DECLARE
  drop_children text;
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'market_events' AND relkind = 'p') THEN
    SELECT string_agg(format('DROP TABLE IF EXISTS %I', c.relname), '; ')
      INTO drop_children
      FROM pg_class c
      JOIN pg_inherits i ON i.inhrelid = c.oid
      JOIN pg_class p ON p.oid = i.inhparent
     WHERE p.relname = 'market_events';

    IF drop_children IS NOT NULL THEN
      EXECUTE drop_children;
    END IF;

    EXECUTE 'DROP TABLE IF EXISTS market_events';
  END IF;

  IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'market_events_old') THEN
    EXECUTE 'ALTER TABLE market_events_old RENAME TO market_events';
  END IF;
END$$;
