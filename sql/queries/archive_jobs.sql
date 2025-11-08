-- name: ArchiveStart :exec
INSERT INTO archive_jobs(table_name, ts_start, ts_end, s3_key, row_count, bytes_written, status)
VALUES ($1, $2, $3, $4, 0, 0, 'running')
ON CONFLICT (table_name, ts_start, ts_end) DO NOTHING;

-- name: ArchiveDone :exec
INSERT INTO archive_jobs(table_name, ts_start, ts_end, s3_key, row_count, bytes_written, status)
VALUES ($1, $2, $3, $4, $5, $6, 'done')
ON CONFLICT (table_name, ts_start, ts_end)
DO UPDATE SET status='done', row_count=$5, bytes_written=$6, s3_key=$4;

-- name: ArchiveFail :exec
INSERT INTO archive_jobs(table_name, ts_start, ts_end, s3_key, row_count, bytes_written, status)
VALUES ($1, $2, $3, $4, 0, 0, 'failed')
ON CONFLICT (table_name, ts_start, ts_end)
DO UPDATE SET status='failed', s3_key=$4;

-- name: ArchiveRecordedDone :one
SELECT EXISTS(
  SELECT 1 FROM archive_jobs
  WHERE table_name = $1 AND ts_start = $2 AND ts_end = $3 AND status = 'done'
) AS recorded;
