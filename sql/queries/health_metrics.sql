-- name: GetPartitionSpan :one
WITH r AS (
  SELECT poly_partition_span($1) AS t
)
SELECT
  (r.t).partition_count::int4         AS partition_count,
  (r.t).oldest_partition::timestamptz AS oldest_partition,
  (r.t).newest_partition::timestamptz AS newest_partition
FROM r;

-- name: SumPartitionSizesMB :one
SELECT poly_sum_partition_sizes_mb($1) AS total_mb;

-- name: LastArchiveDoneEnd :one
SELECT MAX(ts_end)::timestamptz
FROM archive_jobs
WHERE table_name = sqlc.arg(table_name)
  AND status = 'done';

-- name: DbSizePretty :one
SELECT pg_size_pretty(pg_database_size(sqlc.arg(dbname)::text));
