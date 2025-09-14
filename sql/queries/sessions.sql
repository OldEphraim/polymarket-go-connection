-- name: CreateStrategy :one
INSERT INTO strategies (name, config, initial_balance)
VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE 
SET config = EXCLUDED.config
RETURNING *;

-- name: CreateSession :one
INSERT INTO trading_sessions (strategy_id, start_balance, current_balance)
VALUES ($1, $2, $3)
RETURNING *;

-- name: EndSession :exec
UPDATE trading_sessions 
SET ended_at = NOW() 
WHERE id = $1;

-- name: UpdateSessionBalance :exec
UPDATE trading_sessions 
SET current_balance = $1 
WHERE id = $2;

-- name: GetActiveSession :one
SELECT * FROM trading_sessions
WHERE strategy_id = $1 AND ended_at IS NULL
ORDER BY started_at DESC
LIMIT 1;