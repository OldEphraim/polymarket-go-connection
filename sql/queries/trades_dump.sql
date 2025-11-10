-- name: DumpTradesHour :many
SELECT token_id, ts, price, size, aggressor, trade_id
FROM market_trades
WHERE ts >= sqlc.arg(ts_start)::timestamptz
  AND ts  < sqlc.arg(ts_end)  ::timestamptz
ORDER BY ts, id;

-- name: OldestUnarchivedTradesHour :one
SELECT date_trunc('hour', m.ts)::timestamptz AS oldest
FROM market_trades AS m
WHERE NOT EXISTS (
  SELECT 1
  FROM archive_jobs aj
  WHERE aj.table_name = 'market_trades'
    AND aj.status     = 'done'
    AND m.ts >= aj.ts_start AND m.ts < aj.ts_end
)
ORDER BY m.ts
LIMIT 1;
