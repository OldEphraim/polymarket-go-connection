-- name: DumpTradesHour :many
SELECT token_id, ts, price, size, aggressor, trade_id
FROM market_trades
WHERE ts >= sqlc.arg(ts_start)::timestamptz
  AND ts  < sqlc.arg(ts_end)  ::timestamptz
ORDER BY ts, id;

-- name: OldestUnarchivedTradesHour :one
WITH hourly_data AS (
  SELECT date_trunc('hour', ts) AS hour
  FROM market_trades
  GROUP BY 1
)
SELECT CAST(MIN(hour) AS timestamptz) AS oldest;
