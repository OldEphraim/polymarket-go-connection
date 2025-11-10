-- name: DumpQuotesHourPage :many
SELECT token_id, ts, best_bid, best_ask, bid_size1, ask_size1, spread_bps, mid, id
FROM market_quotes
WHERE ts >= sqlc.arg(ts_start)::timestamptz
  AND ts  < sqlc.arg(ts_end)::timestamptz
  AND (
    sqlc.narg(after_ts)::timestamptz IS NULL
    OR (ts, id) > (sqlc.narg(after_ts)::timestamptz, sqlc.narg(after_id)::bigint)
  )
ORDER BY ts, id
LIMIT sqlc.arg(page_limit)::int;

-- name: OldestUnarchivedQuotesHour :one
SELECT date_trunc('hour', m.ts)::timestamptz AS oldest
FROM market_quotes AS m
WHERE NOT EXISTS (
  SELECT 1
  FROM archive_jobs aj
  WHERE aj.table_name = 'market_quotes'
    AND aj.status     = 'done'
    AND m.ts >= aj.ts_start AND m.ts < aj.ts_end
)
ORDER BY m.ts
LIMIT 1;
