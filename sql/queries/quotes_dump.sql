-- name: DumpQuotesHour :many
SELECT token_id, ts, best_bid, best_ask, bid_size1, ask_size1, spread_bps, mid
FROM market_quotes
WHERE ts >= sqlc.arg(ts_start)::timestamptz
  AND ts  < sqlc.arg(ts_end)  ::timestamptz
ORDER BY ts, id;

-- name: OldestUnarchivedQuotesHour :one
WITH hourly_data AS (
  SELECT date_trunc('hour', ts) AS hour
  FROM market_quotes
  GROUP BY 1
)
SELECT CAST(MIN(hour) AS timestamptz) AS oldest;
