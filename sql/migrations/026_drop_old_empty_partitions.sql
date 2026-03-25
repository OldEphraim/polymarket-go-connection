-- +goose Up
-- Safety net: drop old empty partitions regardless of archive status.
-- This prevents empty partition shells from accumulating when the archiver
-- falls behind and the janitor's archive-aware DROP can't run.

-- Generic helper: drops empty child partitions of a given parent table
-- that are older than p_keep_hours.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drop_old_empty_partitions(
  p_parent_table text,
  p_keep_hours   int
)
RETURNS int
LANGUAGE plpgsql
AS $func$
DECLARE
  drop_before timestamptz := date_trunc('hour', now()) - (p_keep_hours * interval '1 hour');
  part        RECORD;
  part_hour   timestamptz;
  dropped     int := 0;
  row_exists  boolean;
  ymdhh       text;
BEGIN
  FOR part IN
    SELECT c.relname AS child_name
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = p_parent_table
  LOOP
    -- Extract the YYYYMMDDHH24 suffix
    ymdhh := regexp_replace(part.child_name, '^' || p_parent_table || '_p', '');
    IF length(ymdhh) <> 10 THEN
      CONTINUE;
    END IF;

    BEGIN
      part_hour := to_timestamp(ymdhh, 'YYYYMMDDHH24');
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    -- Skip partitions within the keep window
    IF part_hour >= drop_before THEN
      CONTINUE;
    END IF;

    -- Check if the partition is empty (fast: just check for one row)
    EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I LIMIT 1)', part.child_name)
      INTO row_exists;

    IF NOT row_exists THEN
      EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RETURN dropped;
END
$func$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS drop_old_empty_partitions(text, int);
