-- name: RecordSignal :one
INSERT INTO market_signals 
(session_id, token_id, signal_type, best_bid, best_ask, 
 bid_liquidity, ask_liquidity, action_reason, confidence)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetRecentSignals :many
SELECT * FROM market_signals
WHERE session_id = $1
ORDER BY timestamp DESC
LIMIT $2;

-- name: GetSignalsByType :many
SELECT * FROM market_signals
WHERE session_id = $1 AND signal_type = $2
ORDER BY timestamp DESC;