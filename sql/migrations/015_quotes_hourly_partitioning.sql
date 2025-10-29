-- +goose Up
-- Convert market_quotes to hourly partitioning

-- 1) Move existing table aside
ALTER TABLE market_quotes RENAME TO market_quotes_old;

-- 2) Create new partitioned table
CREATE TABLE market_quotes (
    id         bigserial NOT NULL,
    token_id   varchar(80) NOT NULL,
    ts         timestamptz DEFAULT now() NOT NULL,
    best_bid   double precision,
    best_ask   double precision,
    bid_size1  double precision,
    ask_size1  double precision,
    spread_bps double precision,
    mid        double precision,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);

-- 3) Create sequence ownership
ALTER SEQUENCE market_quotes_id_seq OWNED BY market_quotes.id;
ALTER TABLE market_quotes ALTER COLUMN id SET DEFAULT nextval('market_quotes_id_seq'::regclass);

-- 4) Helper function for hourly partitions
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_market_quotes_partition_hourly(p_hour timestamptz)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  part_name text := format('market_quotes_p%s', to_char(p_hour, 'YYYYMMDDHH24'));
  start_ts  timestamptz := date_trunc('hour', p_hour);
  end_ts    timestamptz := start_ts + interval '1 hour';
  idx_ts    text := part_name || '_ts';
  idx_tokts text := part_name || '_tok_ts';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_quotes
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.02,
             autovacuum_analyze_scale_factor=0.01)',
    part_name, start_ts, end_ts
  );

  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (ts DESC)', idx_ts, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, ts DESC)', idx_tokts, part_name);
END
$func$;
-- +goose StatementEnd

-- 5) Ensure hourly partitions function
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_quotes_partitions_hourly(p_hours_back int DEFAULT 3,
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
    PERFORM create_market_quotes_partition_hourly(h);
  END LOOP;
END
$func$;
-- +goose StatementEnd

-- 6) Drop archived partitions function
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drop_archived_market_quotes_partitions_hourly(p_keep_hours int)
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
           regexp_replace(c.relname, '^market_quotes_p', '') AS ymdhh
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_quotes'
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

    -- Check if archived
    PERFORM 1
    FROM archive_jobs aj
    WHERE aj.table_name = 'market_quotes'
      AND aj.status = 'done'
      AND aj.ts_start = part_hour
      AND aj.ts_end = part_hour + interval '1 hour';
    
    IF FOUND THEN
      EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RETURN dropped;
END
$func$;
-- +goose StatementEnd

-- 7) Create initial partitions
SELECT ensure_quotes_partitions_hourly(3, 3);

-- 8) Create view for old data
CREATE OR REPLACE VIEW market_quotes_old_view AS
  SELECT * FROM market_quotes_old;

-- +goose Down
DROP VIEW IF EXISTS market_quotes_old_view;
DROP FUNCTION IF EXISTS drop_archived_market_quotes_partitions_hourly(int);
DROP FUNCTION IF EXISTS ensure_quotes_partitions_hourly(int, int);
DROP FUNCTION IF EXISTS create_market_quotes_partition_hourly(timestamptz);

-- Drop child partitions and restore old table
DO $$
DECLARE
  drop_children text;
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'market_quotes' AND relkind = 'p') THEN
    SELECT string_agg(format('DROP TABLE IF EXISTS %I', c.relname), '; ')
      INTO drop_children
      FROM pg_class c
      JOIN pg_inherits i ON i.inhrelid = c.oid
      JOIN pg_class p ON p.oid = i.inhparent
     WHERE p.relname = 'market_quotes';

    IF drop_children IS NOT NULL THEN
      EXECUTE drop_children;
    END IF;

    EXECUTE 'DROP TABLE IF EXISTS market_quotes';
  END IF;

  IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'market_quotes_old') THEN
    EXECUTE 'ALTER TABLE market_quotes_old RENAME TO market_quotes';
  END IF;
END$$;
