-- +goose Up
-- Helpers for (a) precreating day partitions and (b) dropping fully-archived old partitions.

-- (a) ensure_features_partitions: precreate [today - p_days_back … today + p_days_forward]
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_features_partitions(p_days_back int DEFAULT 1,
                                                      p_days_forward int DEFAULT 1)
RETURNS void
LANGUAGE plpgsql
AS $func$
DECLARE
  d date;
BEGIN
  FOR d IN
    SELECT gs::date
    FROM generate_series(current_date - p_days_back,
                         current_date + p_days_forward,
                         interval '1 day') AS gs
  LOOP
    PERFORM create_market_features_partition(d);
  END LOOP;
END
$func$;
-- +goose StatementEnd

-- (b) drop_archived_market_features_partitions: drop whole-day partitions older than keep_days
--     *only if* every hour in that day is present in archive_jobs(status='done')
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drop_archived_market_features_partitions(p_keep_days int)
RETURNS int
LANGUAGE plpgsql
AS $func$
DECLARE
  drop_before date := (current_date - p_keep_days);
  part RECORD;
  dropped int := 0;
  day_start timestamptz;
  day_end   timestamptz;
  hours_missing int;
BEGIN
  FOR part IN
    SELECT c.relname AS child_name,
           regexp_replace(c.relname, '^market_features_p', '') AS ymd
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_features'
  LOOP
    -- parse partition day; skip unexpected names
    BEGIN
      day_start := to_timestamp(part.ymd, 'YYYYMMDD')::date;
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    IF day_start::date >= drop_before THEN
      CONTINUE; -- within retention
    END IF;
    day_end := (day_start + interval '1 day');

    -- Require full 24h coverage by archive_jobs(done)
    WITH hours AS (
      SELECT generate_series(day_start, day_end - interval '1 hour', interval '1 hour') AS h
    )
    SELECT COUNT(*) INTO hours_missing
    FROM hours hh
    WHERE NOT EXISTS (
      SELECT 1
      FROM archive_jobs aj
      WHERE aj.table_name = 'market_features'
        AND aj.status = 'done'
        AND aj.ts_start <= hh.h
        AND aj.ts_end   >  hh.h
    );

    IF hours_missing = 0 THEN
      EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RETURN dropped;
END
$func$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS drop_archived_market_features_partitions(int);
DROP FUNCTION IF EXISTS ensure_features_partitions(int,int);
