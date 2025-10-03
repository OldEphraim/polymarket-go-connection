-- name: UpsertMarketScan :one
INSERT INTO market_scans (
    token_id, event_id, slug, question, 
    last_price, last_volume, liquidity,
    price_24h_ago, volume_24h_ago, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
ON CONFLICT (token_id) DO UPDATE SET
    event_id = EXCLUDED.event_id,
    slug = EXCLUDED.slug,
    question = EXCLUDED.question,
    last_price = EXCLUDED.last_price,
    last_volume = EXCLUDED.last_volume,
    liquidity = EXCLUDED.liquidity,
    last_scanned_at = NOW(),
    scan_count = market_scans.scan_count + 1,
    updated_at = NOW(),
    metadata = EXCLUDED.metadata,
    price_24h_ago = CASE 
        WHEN market_scans.last_scanned_at < NOW() - INTERVAL '24 hours' 
        THEN market_scans.last_price 
        ELSE COALESCE(market_scans.price_24h_ago, market_scans.last_price)
    END,
    volume_24h_ago = CASE 
        WHEN market_scans.last_scanned_at < NOW() - INTERVAL '24 hours' 
        THEN market_scans.last_volume 
        ELSE COALESCE(market_scans.volume_24h_ago, market_scans.last_volume)
    END
RETURNING *;

-- name: RecordMarketEvent :one
INSERT INTO market_events (
    token_id, event_type, old_value, new_value, metadata
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetActiveMarketScans :many
SELECT * FROM market_scans
WHERE is_active = true
ORDER BY last_scanned_at ASC
LIMIT $1;

-- name: GetMarketScan :one
SELECT * FROM market_scans
WHERE token_id = $1;

-- name: DeactivateMarketScan :exec
UPDATE market_scans
SET is_active = false, updated_at = NOW()
WHERE token_id = $1;

-- name: GetRecentMarketEvents :many
SELECT * FROM market_events
WHERE detected_at > NOW() - INTERVAL '1 hour'
ORDER BY detected_at DESC
LIMIT $1;
