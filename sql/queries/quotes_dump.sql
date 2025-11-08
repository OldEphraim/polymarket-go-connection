-- name: DumpQuotesHour :many
SELECT token_id, ts, best_bid, best_ask, bid_size1, ask_size1, spread_bps, mid
FROM market_quotes
WHERE ts >= sqlc.arg(ts_start)::timestamptz
  AND ts  < sqlc.arg(ts_end)  ::timestamptz
ORDER BY ts, id;

-- name: OldestUnarchivedQuotesHour :one
WITH hourly AS (
  SELECT date_trunc('hour', ts) AS h
  FROM market_quotes
  GROUP BY 1
)
SELECT MIN(h)::timestamptz AS oldest
FROM hourly
WHERE NOT EXISTS (
  SELECT 1
  FROM archive_jobs aj
  WHERE aj.table_name = 'market_quotes'
    AND aj.status     = 'done'
    AND h >= aj.ts_start AND h < aj.ts_end
);
