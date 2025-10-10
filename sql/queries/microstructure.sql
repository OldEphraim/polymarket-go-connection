-- === Quotes ===
-- name: InsertQuote :exec
INSERT INTO market_quotes (
  token_id, ts, best_bid, best_ask, bid_size1, ask_size1, spread_bps, mid
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8
);

-- name: GetLatestMid :one
SELECT mid
FROM market_quotes
WHERE token_id = $1
ORDER BY ts DESC
LIMIT 1;

-- === Trades ===
-- name: InsertTrade :exec
INSERT INTO market_trades (
  token_id, ts, price, size, aggressor, trade_id
) VALUES (
  $1, $2, $3, $4, $5, $6
)
ON CONFLICT (trade_id) WHERE trade_id IS NOT NULL DO NOTHING;

-- === Features ===
-- name: UpsertFeatures :exec
INSERT INTO market_features (
  token_id, ts, ret_1m, ret_5m, vol_1m, avg_vol_5m,
  sigma_5m, zscore_5m, imbalance_top, spread_bps,
  broke_high_15m, broke_low_15m, time_to_resolve_h, signed_flow_1m
) VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10,
  $11, $12, $13, $14
)
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

-- === Utility ===
-- name: GetActiveTokenIDs :many
SELECT token_id
FROM market_scans
WHERE is_active = true
ORDER BY last_scanned_at DESC
LIMIT $1;
