-- +goose Up
-- Merge from an arbitrary (temp) stage table. Use regclass to avoid SQL injection.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_from(stage_tbl regclass)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  got_lock boolean;
BEGIN
  -- Optional: keep timeouts conservative even here
  PERFORM set_config('lock_timeout', '1s', true);
  PERFORM set_config('statement_timeout', '30s', true);

  -- No global serialization needed with per-session temp tables,
  -- but keeping a short advisory lock is harmless if you want to avoid
  -- accidental callers pointing at the *same* real table.
  got_lock := true;

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

  -- Clear the temp stage (weak lock)
  EXECUTE format('DELETE FROM %s', stage_tbl::text);
END
$func$;
-- +goose StatementEnd

-- Convenience SQLC target
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION merge_market_features_from_text(stage_tbl text)
RETURNS void
LANGUAGE sql
AS $f$
  SELECT merge_market_features_from(stage_tbl::regclass)
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS merge_market_features_from_text(text);
DROP FUNCTION IF EXISTS merge_market_features_from(regclass);
-- +goose StatementEnd
