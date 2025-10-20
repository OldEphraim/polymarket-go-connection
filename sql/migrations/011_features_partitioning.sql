-- +goose Up

-- 1) Move old heap table aside (no data moved yet).
ALTER TABLE market_features RENAME TO market_features_old;

-- 2) Recreate as a partitioned parent by RANGE(ts).
CREATE TABLE market_features (
    token_id           varchar(80)   NOT NULL,
    ts                 timestamptz   NOT NULL,
    ret_1m             double precision,
    ret_5m             double precision,
    vol_1m             double precision,
    avg_vol_5m         double precision,
    sigma_5m           double precision,
    zscore_5m          double precision,
    imbalance_top      double precision,
    spread_bps         double precision,
    broke_high_15m     boolean,
    broke_low_15m      boolean,
    time_to_resolve_h  double precision,
    signed_flow_1m     double precision,
    PRIMARY KEY (token_id, ts)
) PARTITION BY RANGE (ts);

-- NOTE: no parent “partitioned indexes”. We'll create LOCAL indexes per child.

-- 3) Helper: create a daily partition (with reloptions) and its local indexes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_market_features_partition(p_date date)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  part_name text := format('market_features_p%s', to_char(p_date, 'YYYYMMDD'));
  start_ts  timestamptz := date_trunc('day', p_date)::timestamptz;
  end_ts    timestamptz := (date_trunc('day', p_date) + interval '1 day')::timestamptz;

  idx_ts    text := part_name || '_ts';
  idx_tokts text := part_name || '_tok_ts';
  idx_brin  text := part_name || '_brin_ts';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_features
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.05,
             autovacuum_analyze_scale_factor=0.02)',
    part_name, start_ts, end_ts
  );

  -- Local indexes mirroring old access paths
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (ts DESC)',                       idx_ts,    part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, ts DESC)',              idx_tokts, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I USING brin (ts) WITH (pages_per_range=64)', idx_brin, part_name);
END
$func$;
-- +goose StatementEnd

-- 4) Pre-create yesterday/today/tomorrow so writers have a home.
SELECT create_market_features_partition((current_date - 1));
SELECT create_market_features_partition( current_date       );
SELECT create_market_features_partition((current_date + 1));

-- 5) Convenience view to inspect old rows
CREATE OR REPLACE VIEW market_features_old_view AS
  SELECT * FROM market_features_old;

-- +goose Down

DROP VIEW IF EXISTS market_features_old_view;
DROP FUNCTION IF EXISTS create_market_features_partition(date);

DO $$
DECLARE
  drop_children text;
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'market_features' AND relkind = 'p') THEN
    SELECT string_agg(format('DROP TABLE IF EXISTS %I', c.relname), '; ')
      INTO drop_children
      FROM pg_class c
      JOIN pg_inherits i ON i.inhrelid = c.oid
      JOIN pg_class p ON p.oid = i.inhparent
     WHERE p.relname = 'market_features';

    IF drop_children IS NOT NULL THEN
      EXECUTE drop_children;
    END IF;

    EXECUTE 'DROP TABLE IF EXISTS market_features';
  END IF;

  IF EXISTS (
    SELECT 1 FROM pg_class WHERE relname = 'market_features_old' AND relkind IN ('r','p')
  ) THEN
    EXECUTE 'ALTER TABLE market_features_old RENAME TO market_features';
  END IF;
END$$;
