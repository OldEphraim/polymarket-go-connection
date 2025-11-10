-- name: DumpFeaturesHour :many
SELECT
  token_id, ts, ret_1m, ret_5m, vol_1m, avg_vol_5m, sigma_5m, zscore_5m,
  imbalance_top, spread_bps, broke_high_15m, broke_low_15m, time_to_resolve_h, signed_flow_1m
FROM market_features
WHERE ts >= sqlc.arg(ts_start)::timestamptz
  AND ts  < sqlc.arg(ts_end)  ::timestamptz
ORDER BY ts, token_id;

-- name: OldestUnarchivedFeaturesHour :one
SELECT date_trunc('hour', m.ts)::timestamptz AS oldest
FROM market_features AS m
WHERE NOT EXISTS (
  SELECT 1
  FROM archive_jobs aj
  WHERE aj.table_name = 'market_features'
    AND aj.status     = 'done'
    AND m.ts >= aj.ts_start AND m.ts < aj.ts_end
)
ORDER BY m.ts
LIMIT 1;

