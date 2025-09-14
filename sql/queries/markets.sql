-- name: UpsertMarket :one
INSERT INTO markets (token_id, slug, question, outcome)
VALUES ($1, $2, $3, $4)
ON CONFLICT (token_id) 
DO UPDATE SET 
    slug = EXCLUDED.slug,
    question = EXCLUDED.question,
    updated_at = NOW()
RETURNING *;

-- name: GetMarketByTokenID :one
SELECT * FROM markets WHERE token_id = $1;

-- name: GetActiveMarkets :many
SELECT DISTINCT m.* FROM markets m
JOIN trading_sessions ts ON ts.ended_at IS NULL
JOIN market_signals ms ON ms.token_id = m.token_id
WHERE ms.timestamp > NOW() - INTERVAL '24 hours';
