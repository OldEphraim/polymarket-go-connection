-- +goose Up
-- +goose NO TRANSACTION

-- WAL + checkpoints
ALTER SYSTEM SET wal_compression = 'on';
ALTER SYSTEM SET max_wal_size = '4GB';
ALTER SYSTEM SET min_wal_size = '1GB';
ALTER SYSTEM SET checkpoint_timeout = '15min';
ALTER SYSTEM SET checkpoint_completion_target = '0.9';

-- Buffers (requires restart to take effect)
ALTER SYSTEM SET shared_buffers = '512MB';

-- Autovacuum & maintenance
ALTER SYSTEM SET autovacuum_vacuum_cost_limit = '1000';
ALTER SYSTEM SET autovacuum_max_workers = '5';
ALTER SYSTEM SET autovacuum_naptime = '1min';
ALTER SYSTEM SET maintenance_work_mem = '512MB';

-- NOTE: We intentionally do NOT set effective_io_concurrency here because many local
-- Postgres builds (e.g., macOS/Homebrew) only support 0 and will error if >0 is set.
-- Set it via Docker command-line flags in compose on servers that support it.

-- Apply SIGHUP-level changes now (shared_buffers still needs a restart)
SELECT pg_reload_conf();

-- +goose Down
-- +goose NO TRANSACTION
ALTER SYSTEM RESET wal_compression;
ALTER SYSTEM RESET max_wal_size;
ALTER SYSTEM RESET min_wal_size;
ALTER SYSTEM RESET checkpoint_timeout;
ALTER SYSTEM RESET checkpoint_completion_target;
ALTER SYSTEM RESET shared_buffers;
ALTER SYSTEM RESET autovacuum_vacuum_cost_limit;
ALTER SYSTEM RESET autovacuum_max_workers;
ALTER SYSTEM RESET autovacuum_naptime;
ALTER SYSTEM RESET maintenance_work_mem;

SELECT pg_reload_conf();
