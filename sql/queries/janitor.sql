-- Ensure partitions (hourly)
-- name: EnsureQuotesPartitionsHourly :exec
SELECT ensure_quotes_partitions_hourly(sqlc.arg(back)::int, sqlc.arg(fwd)::int);

-- name: EnsureTradesPartitionsHourly :exec
SELECT ensure_trades_partitions_hourly(sqlc.arg(back)::int, sqlc.arg(fwd)::int);

-- name: EnsureFeaturesPartitionsHourly :exec
SELECT ensure_features_partitions_hourly(sqlc.arg(back)::int, sqlc.arg(fwd)::int);



-- Windowed deletes (rename param to avoid reserved keyword)
-- name: DeleteExportedHoursQuotes :one
SELECT delete_exported_hours_quotes(sqlc.arg(win)::text) AS deleted;

-- name: DeleteExportedHoursTrades :one
SELECT delete_exported_hours_trades(sqlc.arg(win)::text) AS deleted;

-- name: DeleteExportedHoursFeatures :one
SELECT delete_exported_hours_features(sqlc.arg(win)::text) AS deleted;



-- Drop archived partitions
-- name: DropArchivedMarketQuotesPartitionsHourly :one
SELECT drop_archived_market_quotes_partitions_hourly(sqlc.arg(keep_hours)::int) AS dropped;

-- name: DropArchivedMarketTradesPartitionsHourly :one
SELECT drop_archived_market_trades_partitions_hourly(sqlc.arg(keep_hours)::int) AS dropped;

-- name: DropArchivedMarketFeaturesPartitionsHourly :one
SELECT drop_archived_market_features_partitions_hourly(sqlc.arg(keep_hours)::int) AS dropped;



-- Emergency deletes (batched)
-- name: EmergencyDeleteQuotes :one
WITH doomed AS (
  SELECT id
  FROM market_quotes
  WHERE ts < now() - (sqlc.arg(keep_text)::text)::interval
  ORDER BY ts ASC
  LIMIT sqlc.arg(batch)::int
),
del AS (
  DELETE FROM market_quotes q
  USING doomed d
  WHERE q.id = d.id
  RETURNING 1
)
SELECT COALESCE(count(*), 0)::bigint AS deleted;

-- name: EmergencyDeleteTrades :one
WITH doomed AS (
  SELECT id
  FROM market_trades
  WHERE ts < now() - (sqlc.arg(keep_text)::text)::interval
  ORDER BY ts ASC
  LIMIT sqlc.arg(batch)::int
),
del AS (
  DELETE FROM market_trades t
  USING doomed d
  WHERE t.id = d.id
  RETURNING 1
)
SELECT COALESCE(count(*), 0)::bigint AS deleted;

-- name: EmergencyDeleteFeatures :one
WITH doomed AS (
  SELECT token_id, ts
  FROM market_features
  WHERE ts < now() - (sqlc.arg(keep_text)::text)::interval
  ORDER BY ts ASC, token_id ASC
  LIMIT sqlc.arg(batch)::int
),
del AS (
  DELETE FROM market_features f
  USING doomed d
  WHERE f.token_id = d.token_id
    AND f.ts       = d.ts
  RETURNING 1
)
SELECT COALESCE(count(*), 0)::bigint AS deleted;



-- Optional VACUUM helpers
-- name: VacuumQuotes :exec
VACUUM (ANALYZE) market_quotes;

-- name: VacuumTrades :exec
VACUUM (ANALYZE) market_trades;

-- name: VacuumFeatures :exec
VACUUM (ANALYZE) market_features;

-- name: PgCheckpoint :exec
CHECKPOINT;

-- name: PgSwitchWal :one
SELECT pg_switch_wal()::text AS lsn;
