-- +goose Up
-- Serialize merges, avoid TRUNCATE, add conservative timeouts.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_stage()
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  got_lock boolean;
BEGIN
  -- keep the function responsive under lock pressure
  PERFORM set_config('lock_timeout', '1s', true);
  PERFORM set_config('statement_timeout', '15s', true);

  -- serialize merges across all sessions/processes for this key
  got_lock := pg_try_advisory_lock(2147483647);  -- pick any app-wide bigint key
  IF NOT got_lock THEN
    -- a merge is already running elsewhere; skip quietly
    RETURN;
  END IF;

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

    -- Use DELETE instead of TRUNCATE to avoid taking ACCESS EXCLUSIVE.
    DELETE FROM market_features_stage;
  EXCEPTION WHEN OTHERS THEN
    PERFORM pg_advisory_unlock(2147483647);
    RAISE;
  END;

  PERFORM pg_advisory_unlock(2147483647);
END
$func$;
-- +goose StatementEnd

-- +goose Down
-- Revert to TRUNCATE variant (original behavior). Note: this reintroduces stronger locks.
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
