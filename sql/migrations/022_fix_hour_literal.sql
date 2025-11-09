-- +goose Up
-- Make hour merge use a range predicate instead of date_trunc('hour', ...)
-- to avoid dynamic SQL quoting issues.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_from(stage_tbl regclass)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  got_lock boolean;
  hr timestamptz;
BEGIN
  -- Conservative timeouts while merging
  PERFORM set_config('lock_timeout', '10s', true);
  PERFORM set_config('statement_timeout', '60s', true);

  -- Iterate distinct hours present in the stage table
  FOR hr IN EXECUTE format(
    'SELECT DISTINCT date_trunc(''hour'', ts)::timestamptz FROM %s',
    stage_tbl::text
  )
  LOOP
    -- Per-hour advisory lock to reduce cross-session contention
    got_lock := pg_try_advisory_lock(
      hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
    );
    IF NOT got_lock THEN
      CONTINUE;
    END IF;

    BEGIN
      -- Insert/Upsert only the rows from this hour, using a range predicate
      EXECUTE format(
        $q$
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
        WHERE ts >= %L
          AND ts <  %L
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
        -- Only update when values actually change to reduce row-lock churn
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
        $q$,
        stage_tbl::text,
        hr, hr + interval '1 hour'
      );
    EXCEPTION WHEN OTHERS THEN
      PERFORM pg_advisory_unlock(
        hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
      );
      RAISE;
    END;

    PERFORM pg_advisory_unlock(
      hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
    );
  END LOOP;

  -- Clear the stage (weak lock)
  EXECUTE format('DELETE FROM %s', stage_tbl::text);
END
$func$;
-- +goose StatementEnd

-- +goose Down
-- Revert to the previous version that used date_trunc('hour', ts) equality.
-- (This reintroduces the dynamic quoting risk.)
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_from(stage_tbl regclass)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  got_lock boolean;
  hr timestamptz;
BEGIN
  PERFORM set_config('lock_timeout', '10s', true);
  PERFORM set_config('statement_timeout', '60s', true);

  FOR hr IN EXECUTE format(
    'SELECT DISTINCT date_trunc(''hour'', ts)::timestamptz FROM %s',
    stage_tbl::text
  )
  LOOP
    got_lock := pg_try_advisory_lock(
      hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
    );
    IF NOT got_lock THEN
      CONTINUE;
    END IF;

    BEGIN
      EXECUTE format(
        $q$
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
        $q$,
        stage_tbl::text,
        hr
      );
    EXCEPTION WHEN OTHERS THEN
      PERFORM pg_advisory_unlock(
        hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
      );
      RAISE;
    END;

    PERFORM pg_advisory_unlock(
      hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
    );
  END LOOP;

  EXECUTE format('DELETE FROM %s', stage_tbl::text);
END
$func$;
-- +goose StatementEnd
