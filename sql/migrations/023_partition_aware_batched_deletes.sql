-- +goose Up
-- Partition-aware, batched deletes for exported hours.
-- Keeps function names/signatures the same so sqlc + Go do not change.

-- ============================================================
-- Shared helper: run batched deletes against a table by ts window
-- ============================================================
-- We use a common SQL pattern in each function:
--   WITH doomed AS (
--     SELECT <key columns>
--     FROM <table> t
--     WHERE t.ts >= win_start AND t.ts < win_end AND t.ts < cutoff
--     ORDER BY t.ts
--     LIMIT batch
--   )
--   DELETE FROM <table> u USING doomed d
--   WHERE <pk join>
--   RETURNING 1;
--
-- This shape:
--   * lets Postgres prune partitions via the t.ts range
--   * keeps each delete small (reducing WAL/IO spikes)
--   * is easy to loop until rowcount = 0
--
-- NOTE: We keep batch size modest; adjust at the top if your IO is quieter.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_exported_hours_features(p_window TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  cutoff     TIMESTAMPTZ := NOW() - (p_window::interval);
  batch      INT         := 20000;     -- tune: 10k–50k are typical
  n_total    BIGINT      := 0;
  n_step     BIGINT;
  win_start  TIMESTAMPTZ;
  win_end    TIMESTAMPTZ;
BEGIN
  FOR win_start, win_end IN
    SELECT aj.ts_start, aj.ts_end
    FROM archive_jobs aj
    WHERE aj.table_name = 'market_features'
      AND aj.status     = 'done'
      AND aj.ts_start   < cutoff         -- only truly cold windows
    ORDER BY aj.ts_start
  LOOP
    LOOP
      WITH doomed AS (
        SELECT f.token_id, f.ts
        FROM market_features f
        WHERE f.ts >= win_start AND f.ts < win_end
          AND f.ts < cutoff
        ORDER BY f.ts, f.token_id
        LIMIT batch
      ),
      del AS (
        DELETE FROM market_features u
        USING doomed d
        WHERE u.token_id = d.token_id
          AND u.ts       = d.ts
        RETURNING 1
      )
      SELECT COALESCE(count(*),0) INTO n_step FROM del;

      n_total := n_total + n_step;
      EXIT WHEN n_step = 0;
    END LOOP;
  END LOOP;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_features','deleted',n_total,'cutoff',cutoff)::text
  );

  RETURN n_total;
END
$$;
-- +goose StatementEnd


-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_exported_hours_trades(p_window TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  cutoff     TIMESTAMPTZ := NOW() - (p_window::interval);
  batch      INT         := 20000;
  n_total    BIGINT      := 0;
  n_step     BIGINT;
  win_start  TIMESTAMPTZ;
  win_end    TIMESTAMPTZ;
BEGIN
  FOR win_start, win_end IN
    SELECT aj.ts_start, aj.ts_end
    FROM archive_jobs aj
    WHERE aj.table_name = 'market_trades'
      AND aj.status     = 'done'
      AND aj.ts_start   < cutoff
    ORDER BY aj.ts_start
  LOOP
    LOOP
      WITH doomed AS (
        SELECT t.id
        FROM market_trades t
        WHERE t.ts >= win_start AND t.ts < win_end
          AND t.ts < cutoff
        ORDER BY t.ts
        LIMIT batch
      ),
      del AS (
        DELETE FROM market_trades u
        USING doomed d
        WHERE u.id = d.id
        RETURNING 1
      )
      SELECT COALESCE(count(*),0) INTO n_step FROM del;

      n_total := n_total + n_step;
      EXIT WHEN n_step = 0;
    END LOOP;
  END LOOP;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_trades','deleted',n_total,'cutoff',cutoff)::text
  );

  RETURN n_total;
END
$$;
-- +goose StatementEnd


-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_exported_hours_quotes(p_window TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  cutoff     TIMESTAMPTZ := NOW() - (p_window::interval);
  batch      INT         := 20000;
  n_total    BIGINT      := 0;
  n_step     BIGINT;
  win_start  TIMESTAMPTZ;
  win_end    TIMESTAMPTZ;
BEGIN
  FOR win_start, win_end IN
    SELECT aj.ts_start, aj.ts_end
    FROM archive_jobs aj
    WHERE aj.table_name = 'market_quotes'
      AND aj.status     = 'done'
      AND aj.ts_start   < cutoff
    ORDER BY aj.ts_start
  LOOP
    LOOP
      WITH doomed AS (
        SELECT q.id
        FROM market_quotes q
        WHERE q.ts >= win_start AND q.ts < win_end
          AND q.ts < cutoff
        ORDER BY q.ts
        LIMIT batch
      ),
      del AS (
        DELETE FROM market_quotes u
        USING doomed d
        WHERE u.id = d.id
        RETURNING 1
      )
      SELECT COALESCE(count(*),0) INTO n_step FROM del;

      n_total := n_total + n_step;
      EXIT WHEN n_step = 0;
    END LOOP;
  END LOOP;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_quotes','deleted',n_total,'cutoff',cutoff)::text
  );

  RETURN n_total;
END
$$;
-- +goose StatementEnd


-- +goose Down
DROP FUNCTION IF EXISTS delete_exported_hours_quotes(TEXT);
DROP FUNCTION IF EXISTS delete_exported_hours_trades(TEXT);
DROP FUNCTION IF EXISTS delete_exported_hours_features(TEXT);
