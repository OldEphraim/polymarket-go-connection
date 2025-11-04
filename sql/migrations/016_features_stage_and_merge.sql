-- +goose Up
CREATE UNLOGGED TABLE IF NOT EXISTS market_features_stage (
  token_id text NOT NULL,
  ts timestamptz NOT NULL,
  ret_1m double precision,
  ret_5m double precision,
  vol_1m double precision,
  avg_vol_5m double precision,
  sigma_5m double precision,
  zscore_5m double precision,
  imbalance_top double precision,
  spread_bps double precision,
  broke_high_15m boolean,
  broke_low_15m boolean,
  time_to_resolve_h double precision,
  signed_flow_1m double precision
);

CREATE INDEX IF NOT EXISTS idx_mfs_token_ts ON market_features_stage(token_id, ts);

-- Ensure Goose treats the function as a single statement
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_stage()
RETURNS void
LANGUAGE plpgsql
AS $func$
BEGIN
  INSERT INTO market_features AS mf (
    token_id, ts,
    ret_1m, ret_5m,
    vol_1m, avg_vol_5m,
    sigma_5m, zscore_5m,
    imbalance_top,
    spread_bps,
    broke_high_15m, broke_low_15m,
    time_to_resolve_h,
    signed_flow_1m
  )
  SELECT
    token_id, ts,
    ret_1m, ret_5m,
    vol_1m, avg_vol_5m,
    sigma_5m, zscore_5m,
    imbalance_top,
    spread_bps,
    broke_high_15m, broke_low_15m,
    time_to_resolve_h,
    signed_flow_1m
  FROM market_features_stage
  ON CONFLICT (token_id, ts) DO UPDATE SET
    ret_1m = EXCLUDED.ret_1m,
    ret_5m = EXCLUDED.ret_5m,
    vol_1m = EXCLUDED.vol_1m,
    avg_vol_5m = EXCLUDED.avg_vol_5m,
    sigma_5m = EXCLUDED.sigma_5m,
    zscore_5m = EXCLUDED.zscore_5m,
    imbalance_top = EXCLUDED.imbalance_top,
    spread_bps = EXCLUDED.spread_bps,
    broke_high_15m = EXCLUDED.broke_high_15m,
    broke_low_15m = EXCLUDED.broke_low_15m,
    time_to_resolve_h = EXCLUDED.time_to_resolve_h,
    signed_flow_1m = EXCLUDED.signed_flow_1m;

  TRUNCATE TABLE market_features_stage;
END
$func$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS merge_market_features_stage();
-- +goose StatementEnd
DROP TABLE IF EXISTS market_features_stage;
