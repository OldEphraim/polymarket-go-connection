-- 021_per_hour_features_merge.sql
-- Serialize merges per HOUR (matches your hour-partitioning) to avoid hot-key churn.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_from(stage_tbl regclass)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  hr timestamptz;
  hr_key bigint;
BEGIN
  -- Slightly more patient than the previous 1–30s; still conservative.
  PERFORM set_config('lock_timeout', '10s', true);
  PERFORM set_config('statement_timeout', '60s', true);

  -- Iterate the distinct hours present in this session's temp stage.
  FOR hr IN
    EXECUTE format('SELECT DISTINCT date_trunc(''hour'', ts) FROM %s', stage_tbl::text)
  LOOP
    -- Per-hour advisory lock key (64-bit) based on a namespaced, human-friendly token.
    -- This keeps concurrency for different hours, while serializing just the same hour.
    SELECT hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
      INTO hr_key;

    PERFORM pg_advisory_lock(hr_key);
    BEGIN
      EXECUTE format($SQL$
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
        FROM %s
        WHERE date_trunc(''hour'', ts) = %L
        ON CONFLICT (token_id, ts) DO UPDATE
          SET
            ret_1m            = EXCLUDED.ret_1m,
            ret_5m            = EXCLUDED.ret_5m,
            vol_1m            = EXCLUDED.vol_1m,
            avg_vol_5m        = EXCLUDED.avg_vol_5m,
            sigma_5m          = EXCLUDED.sigma_5m,
            zscore_5m         = EXCLUDED.zscore_5m,
            imbalance_top     = EXCLUDED.imbalance_top,
            spread_bps        = EXCLUDED.spread_bps,
            broke_high_15m    = EXCLUDED.broke_high_15m,
            broke_low_15m     = EXCLUDED.broke_low_15m,
            time_to_resolve_h = EXCLUDED.time_to_resolve_h,
            signed_flow_1m    = EXCLUDED.signed_flow_1m
        -- Only perform updates when something actually changes (cuts row-lock churn)
        WHERE (mf.ret_1m            IS DISTINCT FROM EXCLUDED.ret_1m)
           OR (mf.ret_5m            IS DISTINCT FROM EXCLUDED.ret_5m)
           OR (mf.vol_1m            IS DISTINCT FROM EXCLUDED.vol_1m)
           OR (mf.avg_vol_5m        IS DISTINCT FROM EXCLUDED.avg_vol_5m)
           OR (mf.sigma_5m          IS DISTINCT FROM EXCLUDED.sigma_5m)
           OR (mf.zscore_5m         IS DISTINCT FROM EXCLUDED.zscore_5m)
           OR (mf.imbalance_top     IS DISTINCT FROM EXCLUDED.imbalance_top)
           OR (mf.spread_bps        IS DISTINCT FROM EXCLUDED.spread_bps)
           OR (mf.broke_high_15m    IS DISTINCT FROM EXCLUDED.broke_high_15m)
           OR (mf.broke_low_15m     IS DISTINCT FROM EXCLUDED.broke_low_15m)
           OR (mf.time_to_resolve_h IS DISTINCT FROM EXCLUDED.time_to_resolve_h)
           OR (mf.signed_flow_1m    IS DISTINCT FROM EXCLUDED.signed_flow_1m)
      $SQL$, stage_tbl::text, hr);

    EXCEPTION WHEN OTHERS THEN
      PERFORM pg_advisory_unlock(hr_key);
      RAISE;
    END;
    PERFORM pg_advisory_unlock(hr_key);
  END LOOP;

  -- Weak-lock clear of the temp stage (session-local).
  EXECUTE format('DELETE FROM %s', stage_tbl::text);
END
$func$;
-- +goose StatementEnd


-- +goose Down
-- Revert to the simpler "one-shot" merge used in 019 (per your previous body).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_from(stage_tbl regclass)
RETURNS void
LANGUAGE plpgsql
AS $func$
BEGIN
  PERFORM set_config('lock_timeout', '1s', true);
  PERFORM set_config('statement_timeout', '30s', true);

  EXECUTE format(
    'INSERT INTO market_features AS mf (
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
     FROM %s
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
       signed_flow_1m = EXCLUDED.signed_flow_1m',
    stage_tbl::text
  );

  EXECUTE format('DELETE FROM %s', stage_tbl::text);
END
$func$;
-- +goose StatementEnd