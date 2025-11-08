-- name: DumpTradesHour :many
SELECT token_id, ts, price, size, aggressor, trade_id
FROM market_trades
WHERE ts >= sqlc.arg(ts_start)::timestamptz
  AND ts  < sqlc.arg(ts_end)  ::timestamptz
ORDER BY ts, id;

-- name: OldestUnarchivedTradesHour :one
WITH hourly AS (
  SELECT date_trunc('hour', ts) AS h
  FROM market_trades
  GROUP BY 1
)
SELECT MIN(h)::timestamptz AS oldest
FROM hourly
WHERE NOT EXISTS (
  SELECT 1
  FROM archive_jobs aj
  WHERE aj.table_name = 'market_trades'
    AND aj.status     = 'done'
    AND h >= aj.ts_start AND h < aj.ts_end
);
