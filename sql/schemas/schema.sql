--
-- PostgreSQL database dump
--

\restrict da9jZlejdYA5sDxldqbsbN5jSbxGex7ToiYwOd6NsObMiPIZjCVT1pssts15gTj

-- Dumped from database version 14.19 (Homebrew)
-- Dumped by pg_dump version 14.19 (Homebrew)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: create_market_features_partition_hourly(timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.create_market_features_partition_hourly(p_hour timestamp with time zone) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  part_name text := format('market_features_p%s', to_char(p_hour, 'YYYYMMDDHH24'));
  start_ts  timestamptz := date_trunc('hour', p_hour);
  end_ts    timestamptz := start_ts + interval '1 hour';
  idx_ts    text := part_name || '_ts';
  idx_tokts text := part_name || '_tok_ts';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_features
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.01,
             autovacuum_analyze_scale_factor=0.005)',
    part_name, start_ts, end_ts
  );

  -- Minimal indexes for hourly partitions
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (ts DESC)', idx_ts, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, ts DESC)', idx_tokts, part_name);
END
$$;


--
-- Name: create_market_quotes_partition_hourly(timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.create_market_quotes_partition_hourly(p_hour timestamp with time zone) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  part_name text := format('market_quotes_p%s', to_char(p_hour, 'YYYYMMDDHH24'));
  start_ts  timestamptz := date_trunc('hour', p_hour);
  end_ts    timestamptz := start_ts + interval '1 hour';
  idx_ts    text := part_name || '_ts';
  idx_tokts text := part_name || '_tok_ts';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_quotes
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.02,
             autovacuum_analyze_scale_factor=0.01)',
    part_name, start_ts, end_ts
  );

  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (ts DESC)', idx_ts, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, ts DESC)', idx_tokts, part_name);
END
$$;


--
-- Name: create_market_trades_partition_hourly(timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.create_market_trades_partition_hourly(p_hour timestamp with time zone) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  part_name text := format('market_trades_p%s', to_char(p_hour, 'YYYYMMDDHH24'));
  start_ts  timestamptz := date_trunc('hour', p_hour);
  end_ts    timestamptz := start_ts + interval '1 hour';
  idx_ts    text := part_name || '_ts';
  idx_tokts text := part_name || '_tok_ts';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_trades
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.02,
             autovacuum_analyze_scale_factor=0.01)',
    part_name, start_ts, end_ts
  );

  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (ts DESC)', idx_ts, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, ts DESC)', idx_tokts, part_name);
  EXECUTE format('CREATE UNIQUE INDEX IF NOT EXISTS %I ON %I (trade_id) WHERE trade_id IS NOT NULL', 
                 part_name || '_trade_id', part_name);
END
$$;


--
-- Name: create_unique_temp_market_features_stage(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.create_unique_temp_market_features_stage() RETURNS text
    LANGUAGE plpgsql
    AS $$
DECLARE
  tname text := 'mfs_stage_' ||
                to_char(clock_timestamp(), 'YYYYMMDD_HH24MISS') || '_' ||
                lpad(floor(random()*1e9)::bigint::text, 9, '0');
BEGIN
  -- Create a temp table cloned from public.market_features_stage.
  EXECUTE format(
    'CREATE TEMP TABLE %I (LIKE public.market_features_stage INCLUDING ALL) ON COMMIT DROP',
    tname
  );
  -- Return the session-local qualified name
  RETURN 'pg_temp.' || tname;
END
$$;


--
-- Name: delete_exported_hours_features(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.delete_exported_hours_features(p_window text) RETURNS bigint
    LANGUAGE plpgsql
    AS $$
DECLARE
  cutoff TIMESTAMPTZ;
  n BIGINT;
BEGIN
  cutoff := NOW() - (p_window::interval);

  WITH gone AS (
    DELETE FROM market_features mf
    USING archive_jobs aj
    WHERE aj.table_name = 'market_features'
      AND aj.status = 'done'
      AND aj.ts_start < cutoff
      AND mf.ts >= aj.ts_start
      AND mf.ts <  aj.ts_end
    RETURNING 1
  )
  SELECT COUNT(*) INTO n FROM gone;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_features','deleted',n,'cutoff',cutoff)::text
  );

  RETURN n;
END
$$;


--
-- Name: delete_exported_hours_quotes(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.delete_exported_hours_quotes(p_window text) RETURNS bigint
    LANGUAGE plpgsql
    AS $$
DECLARE
  cutoff TIMESTAMPTZ;
  n BIGINT;
BEGIN
  cutoff := NOW() - (p_window::interval);

  WITH gone AS (
    DELETE FROM market_quotes q
    USING archive_jobs aj
    WHERE aj.table_name = 'market_quotes'
      AND aj.status = 'done'
      AND aj.ts_start < cutoff
      AND q.ts >= aj.ts_start
      AND q.ts <  aj.ts_end
    RETURNING 1
  )
  SELECT COUNT(*) INTO n FROM gone;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_quotes','deleted',n,'cutoff',cutoff)::text
  );

  RETURN n;
END
$$;


--
-- Name: delete_exported_hours_trades(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.delete_exported_hours_trades(p_window text) RETURNS bigint
    LANGUAGE plpgsql
    AS $$
DECLARE
  cutoff TIMESTAMPTZ;
  n BIGINT;
BEGIN
  cutoff := NOW() - (p_window::interval);

  WITH gone AS (
    DELETE FROM market_trades t
    USING archive_jobs aj
    WHERE aj.table_name = 'market_trades'
      AND aj.status = 'done'
      AND aj.ts_start < cutoff
      AND t.ts >= aj.ts_start
      AND t.ts <  aj.ts_end
    RETURNING 1
  )
  SELECT COUNT(*) INTO n FROM gone;

  PERFORM pg_notify(
    'janitor',
    json_build_object('table','market_trades','deleted',n,'cutoff',cutoff)::text
  );

  RETURN n;
END
$$;


--
-- Name: drop_archived_market_features_partitions_hourly(integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.drop_archived_market_features_partitions_hourly(p_keep_hours integer) RETURNS integer
    LANGUAGE plpgsql
    AS $$
DECLARE
  drop_before timestamptz := date_trunc('hour', now()) - (p_keep_hours * interval '1 hour');
  part RECORD;
  dropped int := 0;
  part_hour timestamptz;
BEGIN
  FOR part IN
    SELECT c.relname AS child_name,
           regexp_replace(c.relname, '^market_features_p', '') AS ymdhh
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_features'
  LOOP
    -- Parse partition hour from name
    BEGIN
      -- Handle both old daily (YYYYMMDD) and new hourly (YYYYMMDDHH) formats
      IF length(part.ymdhh) = 8 THEN
        -- Daily partition - convert to timestamp
        part_hour := to_timestamp(part.ymdhh || '00', 'YYYYMMDDHH24');
      ELSIF length(part.ymdhh) = 10 THEN
        -- Hourly partition
        part_hour := to_timestamp(part.ymdhh, 'YYYYMMDDHH24');
      ELSE
        CONTINUE;
      END IF;
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    IF part_hour >= drop_before THEN
      CONTINUE; -- within retention
    END IF;

    -- For hourly partitions, check if archived
    IF length(part.ymdhh) = 10 THEN
      -- Check if this hour is archived
      PERFORM 1
      FROM archive_jobs aj
      WHERE aj.table_name = 'market_features'
        AND aj.status = 'done'
        AND aj.ts_start = part_hour
        AND aj.ts_end = part_hour + interval '1 hour';
      
      IF FOUND THEN
        EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
        dropped := dropped + 1;
      END IF;
    ELSIF length(part.ymdhh) = 8 THEN
      -- Old daily partition - drop if older than retention
      EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RETURN dropped;
END
$$;


--
-- Name: drop_archived_market_quotes_partitions_hourly(integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.drop_archived_market_quotes_partitions_hourly(p_keep_hours integer) RETURNS integer
    LANGUAGE plpgsql
    AS $$
DECLARE
  drop_before timestamptz := date_trunc('hour', now()) - (p_keep_hours * interval '1 hour');
  part RECORD;
  dropped int := 0;
  part_hour timestamptz;
BEGIN
  FOR part IN
    SELECT c.relname AS child_name,
           regexp_replace(c.relname, '^market_quotes_p', '') AS ymdhh
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_quotes'
  LOOP
    BEGIN
      IF length(part.ymdhh) = 10 THEN
        part_hour := to_timestamp(part.ymdhh, 'YYYYMMDDHH24');
      ELSE
        CONTINUE;
      END IF;
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    IF part_hour >= drop_before THEN
      CONTINUE;
    END IF;

    -- Check if archived
    PERFORM 1
    FROM archive_jobs aj
    WHERE aj.table_name = 'market_quotes'
      AND aj.status = 'done'
      AND aj.ts_start = part_hour
      AND aj.ts_end = part_hour + interval '1 hour';
    
    IF FOUND THEN
      EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RETURN dropped;
END
$$;


--
-- Name: drop_archived_market_trades_partitions_hourly(integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.drop_archived_market_trades_partitions_hourly(p_keep_hours integer) RETURNS integer
    LANGUAGE plpgsql
    AS $$
DECLARE
  drop_before timestamptz := date_trunc('hour', now()) - (p_keep_hours * interval '1 hour');
  part RECORD;
  dropped int := 0;
  part_hour timestamptz;
BEGIN
  FOR part IN
    SELECT c.relname AS child_name,
           regexp_replace(c.relname, '^market_trades_p', '') AS ymdhh
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_trades'
  LOOP
    BEGIN
      IF length(part.ymdhh) = 10 THEN
        part_hour := to_timestamp(part.ymdhh, 'YYYYMMDDHH24');
      ELSE
        CONTINUE;
      END IF;
    EXCEPTION WHEN others THEN
      CONTINUE;
    END;

    IF part_hour >= drop_before THEN
      CONTINUE;
    END IF;

    -- Check if archived
    PERFORM 1
    FROM archive_jobs aj
    WHERE aj.table_name = 'market_trades'
      AND aj.status = 'done'
      AND aj.ts_start = part_hour
      AND aj.ts_end = part_hour + interval '1 hour';
    
    IF FOUND THEN
      EXECUTE format('DROP TABLE IF EXISTS %I', part.child_name);
      dropped := dropped + 1;
    END IF;
  END LOOP;

  RETURN dropped;
END
$$;


--
-- Name: ensure_features_partitions_hourly(integer, integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ensure_features_partitions_hourly(p_hours_back integer DEFAULT 3, p_hours_forward integer DEFAULT 3) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  h timestamptz;
BEGIN
  FOR h IN
    SELECT generate_series(
      date_trunc('hour', now()) - (p_hours_back * interval '1 hour'),
      date_trunc('hour', now()) + (p_hours_forward * interval '1 hour'),
      interval '1 hour'
    )
  LOOP
    PERFORM create_market_features_partition_hourly(h);
  END LOOP;
END
$$;


--
-- Name: ensure_quotes_partitions_hourly(integer, integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ensure_quotes_partitions_hourly(p_hours_back integer DEFAULT 3, p_hours_forward integer DEFAULT 3) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  h timestamptz;
BEGIN
  FOR h IN
    SELECT generate_series(
      date_trunc('hour', now()) - (p_hours_back * interval '1 hour'),
      date_trunc('hour', now()) + (p_hours_forward * interval '1 hour'),
      interval '1 hour'
    )
  LOOP
    PERFORM create_market_quotes_partition_hourly(h);
  END LOOP;
END
$$;


--
-- Name: ensure_trades_partitions_hourly(integer, integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ensure_trades_partitions_hourly(p_hours_back integer DEFAULT 3, p_hours_forward integer DEFAULT 3) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  h timestamptz;
BEGIN
  FOR h IN
    SELECT generate_series(
      date_trunc('hour', now()) - (p_hours_back * interval '1 hour'),
      date_trunc('hour', now()) + (p_hours_forward * interval '1 hour'),
      interval '1 hour'
    )
  LOOP
    PERFORM create_market_trades_partition_hourly(h);
  END LOOP;
END
$$;


--
-- Name: merge_market_features_from(regclass); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.merge_market_features_from(stage_tbl regclass) RETURNS void
    LANGUAGE plpgsql
    AS $_$
DECLARE
  hr timestamptz;
  hr_key bigint;
BEGIN
  -- Slightly more patient than the previous 1–30s; still conservative.
  PERFORM set_config('lock_timeout', '10s', true);
  PERFORM set_config('statement_timeout', '60s', true);

  -- Iterate the distinct hours present in this session's temp stage.
  FOR hr IN
    EXECUTE format('SELECT DISTINCT date_trunc(''hour'', ts) FROM %s', stage_tbl::text)
  LOOP
    -- Per-hour advisory lock key (64-bit) based on a namespaced, human-friendly token.
    -- This keeps concurrency for different hours, while serializing just the same hour.
    SELECT hashtextextended('market_features_hour:' || to_char(hr, 'YYYY-MM-DD HH24'), 0)
      INTO hr_key;

    PERFORM pg_advisory_lock(hr_key);
    BEGIN
      EXECUTE format($SQL$
        INSERT INTO market_features AS mf (
          token_id, ts,
          ret_1m, ret_5m,
          vol_1m, avg_vol_5m,
          sigma_5m, zscore_5m,
          imbalance_top,
          spread_bps,
          broke_high_15m, broke_low_15m,
          time_to_resolve_h,
          signed_flow_1m
        )
        SELECT
          token_id, ts,
          ret_1m, ret_5m,
          vol_1m, avg_vol_5m,
          sigma_5m, zscore_5m,
          imbalance_top,
          spread_bps,
          broke_high_15m, broke_low_15m,
          time_to_resolve_h,
          signed_flow_1m
        FROM %s
        WHERE date_trunc(''hour'', ts) = %L
        ON CONFLICT (token_id, ts) DO UPDATE
          SET
            ret_1m            = EXCLUDED.ret_1m,
            ret_5m            = EXCLUDED.ret_5m,
            vol_1m            = EXCLUDED.vol_1m,
            avg_vol_5m        = EXCLUDED.avg_vol_5m,
            sigma_5m          = EXCLUDED.sigma_5m,
            zscore_5m         = EXCLUDED.zscore_5m,
            imbalance_top     = EXCLUDED.imbalance_top,
            spread_bps        = EXCLUDED.spread_bps,
            broke_high_15m    = EXCLUDED.broke_high_15m,
            broke_low_15m     = EXCLUDED.broke_low_15m,
            time_to_resolve_h = EXCLUDED.time_to_resolve_h,
            signed_flow_1m    = EXCLUDED.signed_flow_1m
        -- Only perform updates when something actually changes (cuts row-lock churn)
        WHERE (mf.ret_1m            IS DISTINCT FROM EXCLUDED.ret_1m)
           OR (mf.ret_5m            IS DISTINCT FROM EXCLUDED.ret_5m)
           OR (mf.vol_1m            IS DISTINCT FROM EXCLUDED.vol_1m)
           OR (mf.avg_vol_5m        IS DISTINCT FROM EXCLUDED.avg_vol_5m)
           OR (mf.sigma_5m          IS DISTINCT FROM EXCLUDED.sigma_5m)
           OR (mf.zscore_5m         IS DISTINCT FROM EXCLUDED.zscore_5m)
           OR (mf.imbalance_top     IS DISTINCT FROM EXCLUDED.imbalance_top)
           OR (mf.spread_bps        IS DISTINCT FROM EXCLUDED.spread_bps)
           OR (mf.broke_high_15m    IS DISTINCT FROM EXCLUDED.broke_high_15m)
           OR (mf.broke_low_15m     IS DISTINCT FROM EXCLUDED.broke_low_15m)
           OR (mf.time_to_resolve_h IS DISTINCT FROM EXCLUDED.time_to_resolve_h)
           OR (mf.signed_flow_1m    IS DISTINCT FROM EXCLUDED.signed_flow_1m)
      $SQL$, stage_tbl::text, hr);

    EXCEPTION WHEN OTHERS THEN
      PERFORM pg_advisory_unlock(hr_key);
      RAISE;
    END;
    PERFORM pg_advisory_unlock(hr_key);
  END LOOP;

  -- Weak-lock clear of the temp stage (session-local).
  EXECUTE format('DELETE FROM %s', stage_tbl::text);
END
$_$;


--
-- Name: merge_market_features_from_text(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.merge_market_features_from_text(stage_tbl text) RETURNS void
    LANGUAGE sql
    AS $$
  SELECT merge_market_features_from(stage_tbl::regclass)
$$;


--
-- Name: merge_market_features_stage(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.merge_market_features_stage() RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  got_lock boolean;
BEGIN
  -- keep the function responsive under lock pressure
  PERFORM set_config('lock_timeout', '1s', true);
  PERFORM set_config('statement_timeout', '15s', true);

  -- serialize merges across all sessions/processes for this key
  got_lock := pg_try_advisory_lock(2147483647);  -- pick any app-wide bigint key
  IF NOT got_lock THEN
    -- a merge is already running elsewhere; skip quietly
    RETURN;
  END IF;

  BEGIN
    INSERT INTO market_features AS mf (
      token_id, ts,
      ret_1m, ret_5m,
      vol_1m, avg_vol_5m,
      sigma_5m, zscore_5m,
      imbalance_top,
      spread_bps,
      broke_high_15m, broke_low_15m,
      time_to_resolve_h,
      signed_flow_1m
    )
    SELECT
      token_id, ts,
      ret_1m, ret_5m,
      vol_1m, avg_vol_5m,
      sigma_5m, zscore_5m,
      imbalance_top,
      spread_bps,
      broke_high_15m, broke_low_15m,
      time_to_resolve_h,
      signed_flow_1m
    FROM market_features_stage
    ON CONFLICT (token_id, ts) DO UPDATE SET
      ret_1m = EXCLUDED.ret_1m,
      ret_5m = EXCLUDED.ret_5m,
      vol_1m = EXCLUDED.vol_1m,
      avg_vol_5m = EXCLUDED.avg_vol_5m,
      sigma_5m = EXCLUDED.sigma_5m,
      zscore_5m = EXCLUDED.zscore_5m,
      imbalance_top = EXCLUDED.imbalance_top,
      spread_bps = EXCLUDED.spread_bps,
      broke_high_15m = EXCLUDED.broke_high_15m,
      broke_low_15m = EXCLUDED.broke_low_15m,
      time_to_resolve_h = EXCLUDED.time_to_resolve_h,
      signed_flow_1m = EXCLUDED.signed_flow_1m;

    -- Use DELETE instead of TRUNCATE to avoid taking ACCESS EXCLUSIVE.
    DELETE FROM market_features_stage;
  EXCEPTION WHEN OTHERS THEN
    PERFORM pg_advisory_unlock(2147483647);
    RAISE;
  END;

  PERFORM pg_advisory_unlock(2147483647);
END
$$;


--
-- Name: migrate_daily_to_hourly_partitions(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.migrate_daily_to_hourly_partitions() RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  daily_part RECORD;
  row_count bigint;
  h int;
BEGIN
  -- Find daily partitions with data
  FOR daily_part IN
    SELECT c.relname
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    JOIN pg_class p ON p.oid = i.inhparent
    WHERE p.relname = 'market_features'
      AND c.relname LIKE 'market_features_p________' -- 8 digits = daily
  LOOP
    EXECUTE format('SELECT COUNT(*) FROM %I', daily_part.relname) INTO row_count;
    
    IF row_count > 0 THEN
      -- Create hourly partitions for this day
      FOR h IN 0..23 LOOP
        PERFORM create_market_features_partition_hourly(
          to_timestamp(substring(daily_part.relname from 18 for 8) || lpad(h::text, 2, '0'), 'YYYYMMDDHH24')
        );
      END LOOP;
      
      -- Data will automatically route to correct hourly partition on insert
      RAISE NOTICE 'Daily partition % has % rows - create hourly partitions for migration', 
                   daily_part.relname, row_count;
    END IF;
  END LOOP;
END
$$;


--
-- Name: poly_partition_span(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.poly_partition_span(pattern text) RETURNS TABLE(partition_count integer, oldest_partition timestamp with time zone, newest_partition timestamp with time zone)
    LANGUAGE sql STABLE
    AS $$
  WITH partitions AS (
    SELECT
      t.schemaname,
      t.tablename,
      CASE
        -- Strip the 'market_<something>_p' prefix; expect exactly 10 digits YYYYMMDDHH
        WHEN length(regexp_replace(t.tablename, '^market_[[:alnum:]_]+_p', '')) = 10
        THEN to_timestamp(regexp_replace(t.tablename, '^market_[[:alnum:]_]+_p', ''), 'YYYYMMDDHH24')
        ELSE NULL
      END AS partition_time
    FROM pg_catalog.pg_tables AS t
    WHERE t.tablename LIKE pattern
  )
  SELECT
    COUNT(*)::int,
    MIN(partition_time),
    MAX(partition_time)
  FROM partitions
  WHERE partition_time IS NOT NULL;
$$;


--
-- Name: poly_sum_partition_sizes_mb(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.poly_sum_partition_sizes_mb(pattern text) RETURNS double precision
    LANGUAGE sql STABLE
    AS $$
  SELECT COALESCE(
    SUM(pg_total_relation_size(format('%I.%I', t.schemaname, t.tablename)))::float / 1024 / 1024,
    0
  )
  FROM pg_catalog.pg_tables AS t
  WHERE t.tablename LIKE pattern;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: archive_jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.archive_jobs (
    id bigint NOT NULL,
    table_name text NOT NULL,
    ts_start timestamp with time zone NOT NULL,
    ts_end timestamp with time zone NOT NULL,
    s3_key text NOT NULL,
    row_count bigint NOT NULL,
    bytes_written bigint NOT NULL,
    status text DEFAULT 'done'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    CONSTRAINT archive_jobs_time_ok CHECK ((ts_end > ts_start))
);


--
-- Name: archive_jobs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.archive_jobs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: archive_jobs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.archive_jobs_id_seq OWNED BY public.archive_jobs.id;


--
-- Name: goose_db_version; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.goose_db_version (
    id integer NOT NULL,
    version_id bigint NOT NULL,
    is_applied boolean NOT NULL,
    tstamp timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: goose_db_version_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.goose_db_version ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
    SEQUENCE NAME public.goose_db_version_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: market_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_events (
    id integer NOT NULL,
    token_id character varying(80) NOT NULL,
    event_type character varying(50),
    old_value double precision,
    new_value double precision,
    metadata jsonb,
    detected_at timestamp with time zone DEFAULT now()
);


--
-- Name: market_events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.market_events_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: market_events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.market_events_id_seq OWNED BY public.market_events.id;


--
-- Name: market_features; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
PARTITION BY RANGE (ts);


--
-- Name: market_features_old; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_old (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


--
-- Name: market_features_old_view; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.market_features_old_view AS
 SELECT market_features_old.token_id,
    market_features_old.ts,
    market_features_old.ret_1m,
    market_features_old.ret_5m,
    market_features_old.vol_1m,
    market_features_old.avg_vol_5m,
    market_features_old.sigma_5m,
    market_features_old.zscore_5m,
    market_features_old.imbalance_top,
    market_features_old.spread_bps,
    market_features_old.broke_high_15m,
    market_features_old.broke_low_15m,
    market_features_old.time_to_resolve_h,
    market_features_old.signed_flow_1m
   FROM public.market_features_old;


--
-- Name: market_features_p20251018; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p20251018 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


--
-- Name: market_features_p20251019; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p20251019 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


--
-- Name: market_features_p20251020; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p20251020 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


--
-- Name: market_features_p2025102911; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p2025102911 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.01', autovacuum_analyze_scale_factor='0.005');


--
-- Name: market_features_p2025102912; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p2025102912 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.01', autovacuum_analyze_scale_factor='0.005');


--
-- Name: market_features_p2025102913; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p2025102913 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.01', autovacuum_analyze_scale_factor='0.005');


--
-- Name: market_features_p2025102914; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p2025102914 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.01', autovacuum_analyze_scale_factor='0.005');


--
-- Name: market_features_p2025102915; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p2025102915 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.01', autovacuum_analyze_scale_factor='0.005');


--
-- Name: market_features_p2025102916; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p2025102916 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.01', autovacuum_analyze_scale_factor='0.005');


--
-- Name: market_features_p2025102917; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_features_p2025102917 (
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
)
WITH (autovacuum_vacuum_scale_factor='0.01', autovacuum_analyze_scale_factor='0.005');


--
-- Name: market_features_stage; Type: TABLE; Schema: public; Owner: -
--

CREATE UNLOGGED TABLE public.market_features_stage (
    token_id text NOT NULL,
    ts timestamp with time zone NOT NULL,
    ret_1m double precision,
    ret_5m double precision,
    vol_1m double precision,
    avg_vol_5m double precision,
    sigma_5m double precision,
    zscore_5m double precision,
    imbalance_top double precision,
    spread_bps double precision,
    broke_high_15m boolean,
    broke_low_15m boolean,
    time_to_resolve_h double precision,
    signed_flow_1m double precision
);


--
-- Name: market_quotes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes (
    id bigint NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
PARTITION BY RANGE (ts);


--
-- Name: market_quotes_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.market_quotes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: market_quotes_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.market_quotes_id_seq OWNED BY public.market_quotes.id;


--
-- Name: market_quotes_id_seq1; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.market_quotes_id_seq1
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: market_quotes_id_seq1; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.market_quotes_id_seq1 OWNED BY public.market_quotes.id;


--
-- Name: market_quotes_old; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_old (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


--
-- Name: market_quotes_old_view; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.market_quotes_old_view AS
 SELECT market_quotes_old.id,
    market_quotes_old.token_id,
    market_quotes_old.ts,
    market_quotes_old.best_bid,
    market_quotes_old.best_ask,
    market_quotes_old.bid_size1,
    market_quotes_old.ask_size1,
    market_quotes_old.spread_bps,
    market_quotes_old.mid
   FROM public.market_quotes_old;


--
-- Name: market_quotes_p2025102915; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_p2025102915 (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_quotes_p2025102916; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_p2025102916 (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_quotes_p2025102917; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_p2025102917 (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_quotes_p2025102918; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_p2025102918 (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_quotes_p2025102919; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_p2025102919 (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_quotes_p2025102920; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_p2025102920 (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_quotes_p2025102921; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_quotes_p2025102921 (
    id bigint DEFAULT nextval('public.market_quotes_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    best_bid double precision,
    best_ask double precision,
    bid_size1 double precision,
    ask_size1 double precision,
    spread_bps double precision,
    mid double precision
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_scans; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_scans (
    id integer NOT NULL,
    token_id character varying(80) NOT NULL,
    event_id character varying(255),
    slug character varying(255),
    question text,
    last_price double precision,
    last_volume double precision,
    liquidity double precision,
    last_scanned_at timestamp with time zone DEFAULT now(),
    price_24h_ago double precision,
    volume_24h_ago double precision,
    scan_count bigint DEFAULT 0,
    is_active boolean DEFAULT true,
    metadata jsonb,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: market_scans_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.market_scans_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: market_scans_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.market_scans_id_seq OWNED BY public.market_scans.id;


--
-- Name: market_signals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_signals (
    id integer NOT NULL,
    session_id integer,
    token_id character varying(80) NOT NULL,
    signal_type character varying(50) NOT NULL,
    "timestamp" timestamp without time zone DEFAULT now(),
    best_bid numeric(10,6),
    best_ask numeric(10,6),
    bid_liquidity numeric(15,6),
    ask_liquidity numeric(15,6),
    action_reason text,
    confidence numeric(5,2)
);


--
-- Name: market_signals_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.market_signals_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: market_signals_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.market_signals_id_seq OWNED BY public.market_signals.id;


--
-- Name: market_trades; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades (
    id bigint NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
PARTITION BY RANGE (ts);


--
-- Name: market_trades_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.market_trades_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: market_trades_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.market_trades_id_seq OWNED BY public.market_trades.id;


--
-- Name: market_trades_id_seq1; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.market_trades_id_seq1
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: market_trades_id_seq1; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.market_trades_id_seq1 OWNED BY public.market_trades.id;


--
-- Name: market_trades_old; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_old (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


--
-- Name: market_trades_old_view; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.market_trades_old_view AS
 SELECT market_trades_old.id,
    market_trades_old.token_id,
    market_trades_old.ts,
    market_trades_old.price,
    market_trades_old.size,
    market_trades_old.aggressor,
    market_trades_old.trade_id
   FROM public.market_trades_old;


--
-- Name: market_trades_p2025102914; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_p2025102914 (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_trades_p2025102915; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_p2025102915 (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_trades_p2025102916; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_p2025102916 (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_trades_p2025102917; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_p2025102917 (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_trades_p2025102918; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_p2025102918 (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_trades_p2025102919; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_p2025102919 (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: market_trades_p2025102920; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_trades_p2025102920 (
    id bigint DEFAULT nextval('public.market_trades_id_seq'::regclass) NOT NULL,
    token_id character varying(80) NOT NULL,
    ts timestamp with time zone NOT NULL,
    price double precision NOT NULL,
    size double precision NOT NULL,
    aggressor text,
    trade_id text,
    CONSTRAINT market_trades_aggressor_check1 CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.02', autovacuum_analyze_scale_factor='0.01');


--
-- Name: markets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.markets (
    id integer NOT NULL,
    token_id character varying(80) NOT NULL,
    slug character varying(255),
    question text,
    outcome character varying(50),
    created_at timestamp without time zone DEFAULT now(),
    updated_at timestamp without time zone DEFAULT now()
);


--
-- Name: markets_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.markets_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: markets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.markets_id_seq OWNED BY public.markets.id;


--
-- Name: paper_orders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_orders (
    id integer NOT NULL,
    session_id integer,
    signal_id integer,
    token_id character varying(80) NOT NULL,
    side character varying(10) NOT NULL,
    price numeric(10,6) NOT NULL,
    size numeric(15,6) NOT NULL,
    status character varying(20) DEFAULT 'open'::character varying,
    created_at timestamp without time zone DEFAULT now(),
    filled_at timestamp without time zone
);


--
-- Name: paper_orders_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.paper_orders_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: paper_orders_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.paper_orders_id_seq OWNED BY public.paper_orders.id;


--
-- Name: paper_positions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_positions (
    id integer NOT NULL,
    session_id integer,
    token_id character varying(80) NOT NULL,
    shares numeric(15,6) NOT NULL,
    avg_entry_price numeric(10,6),
    current_price numeric(10,6),
    unrealized_pnl numeric(15,6),
    updated_at timestamp without time zone DEFAULT now()
);


--
-- Name: paper_positions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.paper_positions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: paper_positions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.paper_positions_id_seq OWNED BY public.paper_positions.id;


--
-- Name: paper_trades; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_trades (
    id bigint NOT NULL,
    session_id integer NOT NULL,
    token_id character varying(80) NOT NULL,
    side character varying(10) NOT NULL,
    price numeric(10,6) NOT NULL,
    shares numeric(15,6) NOT NULL,
    notional numeric(15,6) NOT NULL,
    realized_pnl numeric(15,6) DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: paper_trades_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.paper_trades_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: paper_trades_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.paper_trades_id_seq OWNED BY public.paper_trades.id;


--
-- Name: strategies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.strategies (
    id integer NOT NULL,
    name character varying(100) NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    initial_balance numeric(15,6) DEFAULT 1000.00,
    active boolean DEFAULT true,
    created_at timestamp without time zone DEFAULT now()
);


--
-- Name: strategies_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.strategies_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: strategies_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.strategies_id_seq OWNED BY public.strategies.id;


--
-- Name: trading_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.trading_sessions (
    id integer NOT NULL,
    strategy_id integer,
    start_balance numeric(15,6),
    current_balance numeric(15,6),
    started_at timestamp without time zone DEFAULT now(),
    ended_at timestamp without time zone,
    realized_pnl numeric(15,6) DEFAULT 0 NOT NULL
);


--
-- Name: trading_sessions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.trading_sessions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: trading_sessions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.trading_sessions_id_seq OWNED BY public.trading_sessions.id;


--
-- Name: market_features_p20251018; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p20251018 FOR VALUES FROM ('2025-10-18 00:00:00-04') TO ('2025-10-19 00:00:00-04');


--
-- Name: market_features_p20251019; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p20251019 FOR VALUES FROM ('2025-10-19 00:00:00-04') TO ('2025-10-20 00:00:00-04');


--
-- Name: market_features_p20251020; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p20251020 FOR VALUES FROM ('2025-10-20 00:00:00-04') TO ('2025-10-21 00:00:00-04');


--
-- Name: market_features_p2025102911; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p2025102911 FOR VALUES FROM ('2025-10-29 11:00:00-04') TO ('2025-10-29 12:00:00-04');


--
-- Name: market_features_p2025102912; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p2025102912 FOR VALUES FROM ('2025-10-29 12:00:00-04') TO ('2025-10-29 13:00:00-04');


--
-- Name: market_features_p2025102913; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p2025102913 FOR VALUES FROM ('2025-10-29 13:00:00-04') TO ('2025-10-29 14:00:00-04');


--
-- Name: market_features_p2025102914; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p2025102914 FOR VALUES FROM ('2025-10-29 14:00:00-04') TO ('2025-10-29 15:00:00-04');


--
-- Name: market_features_p2025102915; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p2025102915 FOR VALUES FROM ('2025-10-29 15:00:00-04') TO ('2025-10-29 16:00:00-04');


--
-- Name: market_features_p2025102916; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p2025102916 FOR VALUES FROM ('2025-10-29 16:00:00-04') TO ('2025-10-29 17:00:00-04');


--
-- Name: market_features_p2025102917; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features ATTACH PARTITION public.market_features_p2025102917 FOR VALUES FROM ('2025-10-29 17:00:00-04') TO ('2025-10-29 18:00:00-04');


--
-- Name: market_quotes_p2025102915; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ATTACH PARTITION public.market_quotes_p2025102915 FOR VALUES FROM ('2025-10-29 15:00:00-04') TO ('2025-10-29 16:00:00-04');


--
-- Name: market_quotes_p2025102916; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ATTACH PARTITION public.market_quotes_p2025102916 FOR VALUES FROM ('2025-10-29 16:00:00-04') TO ('2025-10-29 17:00:00-04');


--
-- Name: market_quotes_p2025102917; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ATTACH PARTITION public.market_quotes_p2025102917 FOR VALUES FROM ('2025-10-29 17:00:00-04') TO ('2025-10-29 18:00:00-04');


--
-- Name: market_quotes_p2025102918; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ATTACH PARTITION public.market_quotes_p2025102918 FOR VALUES FROM ('2025-10-29 18:00:00-04') TO ('2025-10-29 19:00:00-04');


--
-- Name: market_quotes_p2025102919; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ATTACH PARTITION public.market_quotes_p2025102919 FOR VALUES FROM ('2025-10-29 19:00:00-04') TO ('2025-10-29 20:00:00-04');


--
-- Name: market_quotes_p2025102920; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ATTACH PARTITION public.market_quotes_p2025102920 FOR VALUES FROM ('2025-10-29 20:00:00-04') TO ('2025-10-29 21:00:00-04');


--
-- Name: market_quotes_p2025102921; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ATTACH PARTITION public.market_quotes_p2025102921 FOR VALUES FROM ('2025-10-29 21:00:00-04') TO ('2025-10-29 22:00:00-04');


--
-- Name: market_trades_p2025102914; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ATTACH PARTITION public.market_trades_p2025102914 FOR VALUES FROM ('2025-10-29 14:00:00-04') TO ('2025-10-29 15:00:00-04');


--
-- Name: market_trades_p2025102915; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ATTACH PARTITION public.market_trades_p2025102915 FOR VALUES FROM ('2025-10-29 15:00:00-04') TO ('2025-10-29 16:00:00-04');


--
-- Name: market_trades_p2025102916; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ATTACH PARTITION public.market_trades_p2025102916 FOR VALUES FROM ('2025-10-29 16:00:00-04') TO ('2025-10-29 17:00:00-04');


--
-- Name: market_trades_p2025102917; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ATTACH PARTITION public.market_trades_p2025102917 FOR VALUES FROM ('2025-10-29 17:00:00-04') TO ('2025-10-29 18:00:00-04');


--
-- Name: market_trades_p2025102918; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ATTACH PARTITION public.market_trades_p2025102918 FOR VALUES FROM ('2025-10-29 18:00:00-04') TO ('2025-10-29 19:00:00-04');


--
-- Name: market_trades_p2025102919; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ATTACH PARTITION public.market_trades_p2025102919 FOR VALUES FROM ('2025-10-29 19:00:00-04') TO ('2025-10-29 20:00:00-04');


--
-- Name: market_trades_p2025102920; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ATTACH PARTITION public.market_trades_p2025102920 FOR VALUES FROM ('2025-10-29 20:00:00-04') TO ('2025-10-29 21:00:00-04');


--
-- Name: archive_jobs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.archive_jobs ALTER COLUMN id SET DEFAULT nextval('public.archive_jobs_id_seq'::regclass);


--
-- Name: market_events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_events ALTER COLUMN id SET DEFAULT nextval('public.market_events_id_seq'::regclass);


--
-- Name: market_quotes id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes ALTER COLUMN id SET DEFAULT nextval('public.market_quotes_id_seq'::regclass);


--
-- Name: market_scans id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_scans ALTER COLUMN id SET DEFAULT nextval('public.market_scans_id_seq'::regclass);


--
-- Name: market_signals id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_signals ALTER COLUMN id SET DEFAULT nextval('public.market_signals_id_seq'::regclass);


--
-- Name: market_trades id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades ALTER COLUMN id SET DEFAULT nextval('public.market_trades_id_seq'::regclass);


--
-- Name: markets id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.markets ALTER COLUMN id SET DEFAULT nextval('public.markets_id_seq'::regclass);


--
-- Name: paper_orders id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_orders ALTER COLUMN id SET DEFAULT nextval('public.paper_orders_id_seq'::regclass);


--
-- Name: paper_positions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_positions ALTER COLUMN id SET DEFAULT nextval('public.paper_positions_id_seq'::regclass);


--
-- Name: paper_trades id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_trades ALTER COLUMN id SET DEFAULT nextval('public.paper_trades_id_seq'::regclass);


--
-- Name: strategies id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategies ALTER COLUMN id SET DEFAULT nextval('public.strategies_id_seq'::regclass);


--
-- Name: trading_sessions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trading_sessions ALTER COLUMN id SET DEFAULT nextval('public.trading_sessions_id_seq'::regclass);


--
-- Name: archive_jobs archive_jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.archive_jobs
    ADD CONSTRAINT archive_jobs_pkey PRIMARY KEY (id);


--
-- Name: archive_jobs archive_jobs_table_name_ts_start_ts_end_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.archive_jobs
    ADD CONSTRAINT archive_jobs_table_name_ts_start_ts_end_key UNIQUE (table_name, ts_start, ts_end);


--
-- Name: goose_db_version goose_db_version_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.goose_db_version
    ADD CONSTRAINT goose_db_version_pkey PRIMARY KEY (id);


--
-- Name: market_events market_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_events
    ADD CONSTRAINT market_events_pkey PRIMARY KEY (id);


--
-- Name: market_features market_features_pkey1; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features
    ADD CONSTRAINT market_features_pkey1 PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p20251018 market_features_p20251018_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p20251018
    ADD CONSTRAINT market_features_p20251018_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p20251019 market_features_p20251019_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p20251019
    ADD CONSTRAINT market_features_p20251019_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p20251020 market_features_p20251020_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p20251020
    ADD CONSTRAINT market_features_p20251020_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p2025102911 market_features_p2025102911_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p2025102911
    ADD CONSTRAINT market_features_p2025102911_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p2025102912 market_features_p2025102912_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p2025102912
    ADD CONSTRAINT market_features_p2025102912_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p2025102913 market_features_p2025102913_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p2025102913
    ADD CONSTRAINT market_features_p2025102913_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p2025102914 market_features_p2025102914_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p2025102914
    ADD CONSTRAINT market_features_p2025102914_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p2025102915 market_features_p2025102915_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p2025102915
    ADD CONSTRAINT market_features_p2025102915_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p2025102916 market_features_p2025102916_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p2025102916
    ADD CONSTRAINT market_features_p2025102916_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_p2025102917 market_features_p2025102917_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_p2025102917
    ADD CONSTRAINT market_features_p2025102917_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_features_old market_features_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_old
    ADD CONSTRAINT market_features_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_quotes market_quotes_pkey1; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes
    ADD CONSTRAINT market_quotes_pkey1 PRIMARY KEY (id, ts);


--
-- Name: market_quotes_p2025102915 market_quotes_p2025102915_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_p2025102915
    ADD CONSTRAINT market_quotes_p2025102915_pkey PRIMARY KEY (id, ts);


--
-- Name: market_quotes_p2025102916 market_quotes_p2025102916_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_p2025102916
    ADD CONSTRAINT market_quotes_p2025102916_pkey PRIMARY KEY (id, ts);


--
-- Name: market_quotes_p2025102917 market_quotes_p2025102917_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_p2025102917
    ADD CONSTRAINT market_quotes_p2025102917_pkey PRIMARY KEY (id, ts);


--
-- Name: market_quotes_p2025102918 market_quotes_p2025102918_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_p2025102918
    ADD CONSTRAINT market_quotes_p2025102918_pkey PRIMARY KEY (id, ts);


--
-- Name: market_quotes_p2025102919 market_quotes_p2025102919_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_p2025102919
    ADD CONSTRAINT market_quotes_p2025102919_pkey PRIMARY KEY (id, ts);


--
-- Name: market_quotes_p2025102920 market_quotes_p2025102920_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_p2025102920
    ADD CONSTRAINT market_quotes_p2025102920_pkey PRIMARY KEY (id, ts);


--
-- Name: market_quotes_p2025102921 market_quotes_p2025102921_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_p2025102921
    ADD CONSTRAINT market_quotes_p2025102921_pkey PRIMARY KEY (id, ts);


--
-- Name: market_quotes_old market_quotes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes_old
    ADD CONSTRAINT market_quotes_pkey PRIMARY KEY (id);


--
-- Name: market_scans market_scans_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_scans
    ADD CONSTRAINT market_scans_pkey PRIMARY KEY (id);


--
-- Name: market_scans market_scans_token_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_scans
    ADD CONSTRAINT market_scans_token_id_key UNIQUE (token_id);


--
-- Name: market_signals market_signals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_signals
    ADD CONSTRAINT market_signals_pkey PRIMARY KEY (id);


--
-- Name: market_trades market_trades_pkey1; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades
    ADD CONSTRAINT market_trades_pkey1 PRIMARY KEY (id, ts);


--
-- Name: market_trades_p2025102914 market_trades_p2025102914_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_p2025102914
    ADD CONSTRAINT market_trades_p2025102914_pkey PRIMARY KEY (id, ts);


--
-- Name: market_trades_p2025102915 market_trades_p2025102915_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_p2025102915
    ADD CONSTRAINT market_trades_p2025102915_pkey PRIMARY KEY (id, ts);


--
-- Name: market_trades_p2025102916 market_trades_p2025102916_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_p2025102916
    ADD CONSTRAINT market_trades_p2025102916_pkey PRIMARY KEY (id, ts);


--
-- Name: market_trades_p2025102917 market_trades_p2025102917_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_p2025102917
    ADD CONSTRAINT market_trades_p2025102917_pkey PRIMARY KEY (id, ts);


--
-- Name: market_trades_p2025102918 market_trades_p2025102918_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_p2025102918
    ADD CONSTRAINT market_trades_p2025102918_pkey PRIMARY KEY (id, ts);


--
-- Name: market_trades_p2025102919 market_trades_p2025102919_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_p2025102919
    ADD CONSTRAINT market_trades_p2025102919_pkey PRIMARY KEY (id, ts);


--
-- Name: market_trades_p2025102920 market_trades_p2025102920_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_p2025102920
    ADD CONSTRAINT market_trades_p2025102920_pkey PRIMARY KEY (id, ts);


--
-- Name: market_trades_old market_trades_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades_old
    ADD CONSTRAINT market_trades_pkey PRIMARY KEY (id);


--
-- Name: markets markets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.markets
    ADD CONSTRAINT markets_pkey PRIMARY KEY (id);


--
-- Name: markets markets_token_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.markets
    ADD CONSTRAINT markets_token_id_key UNIQUE (token_id);


--
-- Name: paper_orders paper_orders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_orders
    ADD CONSTRAINT paper_orders_pkey PRIMARY KEY (id);


--
-- Name: paper_positions paper_positions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_positions
    ADD CONSTRAINT paper_positions_pkey PRIMARY KEY (id);


--
-- Name: paper_positions paper_positions_session_id_token_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_positions
    ADD CONSTRAINT paper_positions_session_id_token_id_key UNIQUE (session_id, token_id);


--
-- Name: paper_trades paper_trades_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_trades
    ADD CONSTRAINT paper_trades_pkey PRIMARY KEY (id);


--
-- Name: strategies strategies_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategies
    ADD CONSTRAINT strategies_name_key UNIQUE (name);


--
-- Name: strategies strategies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategies
    ADD CONSTRAINT strategies_pkey PRIMARY KEY (id);


--
-- Name: trading_sessions trading_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trading_sessions
    ADD CONSTRAINT trading_sessions_pkey PRIMARY KEY (id);


--
-- Name: brin_market_events_detected; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX brin_market_events_detected ON public.market_events USING brin (detected_at) WITH (pages_per_range='64');


--
-- Name: brin_market_features_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX brin_market_features_ts ON public.market_features_old USING brin (ts) WITH (pages_per_range='64');


--
-- Name: brin_market_quotes_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX brin_market_quotes_ts ON public.market_quotes_old USING brin (ts) WITH (pages_per_range='64');


--
-- Name: brin_market_trades_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX brin_market_trades_ts ON public.market_trades_old USING brin (ts) WITH (pages_per_range='64');


--
-- Name: idx_active_market_scans; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_active_market_scans ON public.market_scans USING btree (is_active, last_scanned_at);


--
-- Name: idx_archive_jobs_table_ts_done; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_archive_jobs_table_ts_done ON public.archive_jobs USING btree (table_name, ts_start) WHERE (status = 'done'::text);


--
-- Name: idx_market_events_detected; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_events_detected ON public.market_events USING btree (detected_at DESC);


--
-- Name: idx_market_events_token; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_events_token ON public.market_events USING btree (token_id, detected_at DESC);


--
-- Name: idx_market_events_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_events_type ON public.market_events USING btree (event_type, detected_at DESC);


--
-- Name: idx_market_features_token_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_features_token_ts ON public.market_features_old USING btree (token_id, ts DESC);


--
-- Name: idx_market_features_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_features_ts ON public.market_features_old USING btree (ts DESC);


--
-- Name: idx_market_quotes_token_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_quotes_token_ts ON public.market_quotes_old USING btree (token_id, ts DESC);


--
-- Name: idx_market_quotes_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_quotes_ts ON public.market_quotes_old USING btree (ts DESC);


--
-- Name: idx_market_scans_price_change; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_scans_price_change ON public.market_scans USING btree (((last_price - price_24h_ago))) WHERE (is_active = true);


--
-- Name: idx_market_scans_volume; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_scans_volume ON public.market_scans USING btree (last_volume DESC) WHERE (is_active = true);


--
-- Name: idx_market_trades_token_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_trades_token_ts ON public.market_trades_old USING btree (token_id, ts DESC);


--
-- Name: idx_market_trades_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_trades_ts ON public.market_trades_old USING btree (ts DESC);


--
-- Name: idx_mfs_token_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mfs_token_ts ON public.market_features_stage USING btree (token_id, ts);


--
-- Name: idx_paper_trades_session_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_paper_trades_session_time ON public.paper_trades USING btree (session_id, created_at DESC);


--
-- Name: idx_paper_trades_token_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_paper_trades_token_time ON public.paper_trades USING btree (token_id, created_at DESC);


--
-- Name: market_features_p20251018_brin_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251018_brin_ts ON public.market_features_p20251018 USING brin (ts) WITH (pages_per_range='64');


--
-- Name: market_features_p20251018_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251018_tok_ts ON public.market_features_p20251018 USING btree (token_id, ts DESC);


--
-- Name: market_features_p20251018_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251018_ts ON public.market_features_p20251018 USING btree (ts DESC);


--
-- Name: market_features_p20251019_brin_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251019_brin_ts ON public.market_features_p20251019 USING brin (ts) WITH (pages_per_range='64');


--
-- Name: market_features_p20251019_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251019_tok_ts ON public.market_features_p20251019 USING btree (token_id, ts DESC);


--
-- Name: market_features_p20251019_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251019_ts ON public.market_features_p20251019 USING btree (ts DESC);


--
-- Name: market_features_p20251020_brin_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251020_brin_ts ON public.market_features_p20251020 USING brin (ts) WITH (pages_per_range='64');


--
-- Name: market_features_p20251020_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251020_tok_ts ON public.market_features_p20251020 USING btree (token_id, ts DESC);


--
-- Name: market_features_p20251020_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p20251020_ts ON public.market_features_p20251020 USING btree (ts DESC);


--
-- Name: market_features_p2025102911_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102911_tok_ts ON public.market_features_p2025102911 USING btree (token_id, ts DESC);


--
-- Name: market_features_p2025102911_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102911_ts ON public.market_features_p2025102911 USING btree (ts DESC);


--
-- Name: market_features_p2025102912_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102912_tok_ts ON public.market_features_p2025102912 USING btree (token_id, ts DESC);


--
-- Name: market_features_p2025102912_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102912_ts ON public.market_features_p2025102912 USING btree (ts DESC);


--
-- Name: market_features_p2025102913_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102913_tok_ts ON public.market_features_p2025102913 USING btree (token_id, ts DESC);


--
-- Name: market_features_p2025102913_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102913_ts ON public.market_features_p2025102913 USING btree (ts DESC);


--
-- Name: market_features_p2025102914_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102914_tok_ts ON public.market_features_p2025102914 USING btree (token_id, ts DESC);


--
-- Name: market_features_p2025102914_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102914_ts ON public.market_features_p2025102914 USING btree (ts DESC);


--
-- Name: market_features_p2025102915_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102915_tok_ts ON public.market_features_p2025102915 USING btree (token_id, ts DESC);


--
-- Name: market_features_p2025102915_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102915_ts ON public.market_features_p2025102915 USING btree (ts DESC);


--
-- Name: market_features_p2025102916_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102916_tok_ts ON public.market_features_p2025102916 USING btree (token_id, ts DESC);


--
-- Name: market_features_p2025102916_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102916_ts ON public.market_features_p2025102916 USING btree (ts DESC);


--
-- Name: market_features_p2025102917_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102917_tok_ts ON public.market_features_p2025102917 USING btree (token_id, ts DESC);


--
-- Name: market_features_p2025102917_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_features_p2025102917_ts ON public.market_features_p2025102917 USING btree (ts DESC);


--
-- Name: market_quotes_p2025102915_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102915_tok_ts ON public.market_quotes_p2025102915 USING btree (token_id, ts DESC);


--
-- Name: market_quotes_p2025102915_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102915_ts ON public.market_quotes_p2025102915 USING btree (ts DESC);


--
-- Name: market_quotes_p2025102916_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102916_tok_ts ON public.market_quotes_p2025102916 USING btree (token_id, ts DESC);


--
-- Name: market_quotes_p2025102916_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102916_ts ON public.market_quotes_p2025102916 USING btree (ts DESC);


--
-- Name: market_quotes_p2025102917_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102917_tok_ts ON public.market_quotes_p2025102917 USING btree (token_id, ts DESC);


--
-- Name: market_quotes_p2025102917_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102917_ts ON public.market_quotes_p2025102917 USING btree (ts DESC);


--
-- Name: market_quotes_p2025102918_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102918_tok_ts ON public.market_quotes_p2025102918 USING btree (token_id, ts DESC);


--
-- Name: market_quotes_p2025102918_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102918_ts ON public.market_quotes_p2025102918 USING btree (ts DESC);


--
-- Name: market_quotes_p2025102919_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102919_tok_ts ON public.market_quotes_p2025102919 USING btree (token_id, ts DESC);


--
-- Name: market_quotes_p2025102919_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102919_ts ON public.market_quotes_p2025102919 USING btree (ts DESC);


--
-- Name: market_quotes_p2025102920_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102920_tok_ts ON public.market_quotes_p2025102920 USING btree (token_id, ts DESC);


--
-- Name: market_quotes_p2025102920_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102920_ts ON public.market_quotes_p2025102920 USING btree (ts DESC);


--
-- Name: market_quotes_p2025102921_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102921_tok_ts ON public.market_quotes_p2025102921 USING btree (token_id, ts DESC);


--
-- Name: market_quotes_p2025102921_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_quotes_p2025102921_ts ON public.market_quotes_p2025102921 USING btree (ts DESC);


--
-- Name: market_trades_p2025102914_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102914_tok_ts ON public.market_trades_p2025102914 USING btree (token_id, ts DESC);


--
-- Name: market_trades_p2025102914_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX market_trades_p2025102914_trade_id ON public.market_trades_p2025102914 USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_trades_p2025102914_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102914_ts ON public.market_trades_p2025102914 USING btree (ts DESC);


--
-- Name: market_trades_p2025102915_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102915_tok_ts ON public.market_trades_p2025102915 USING btree (token_id, ts DESC);


--
-- Name: market_trades_p2025102915_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX market_trades_p2025102915_trade_id ON public.market_trades_p2025102915 USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_trades_p2025102915_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102915_ts ON public.market_trades_p2025102915 USING btree (ts DESC);


--
-- Name: market_trades_p2025102916_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102916_tok_ts ON public.market_trades_p2025102916 USING btree (token_id, ts DESC);


--
-- Name: market_trades_p2025102916_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX market_trades_p2025102916_trade_id ON public.market_trades_p2025102916 USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_trades_p2025102916_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102916_ts ON public.market_trades_p2025102916 USING btree (ts DESC);


--
-- Name: market_trades_p2025102917_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102917_tok_ts ON public.market_trades_p2025102917 USING btree (token_id, ts DESC);


--
-- Name: market_trades_p2025102917_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX market_trades_p2025102917_trade_id ON public.market_trades_p2025102917 USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_trades_p2025102917_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102917_ts ON public.market_trades_p2025102917 USING btree (ts DESC);


--
-- Name: market_trades_p2025102918_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102918_tok_ts ON public.market_trades_p2025102918 USING btree (token_id, ts DESC);


--
-- Name: market_trades_p2025102918_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX market_trades_p2025102918_trade_id ON public.market_trades_p2025102918 USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_trades_p2025102918_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102918_ts ON public.market_trades_p2025102918 USING btree (ts DESC);


--
-- Name: market_trades_p2025102919_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102919_tok_ts ON public.market_trades_p2025102919 USING btree (token_id, ts DESC);


--
-- Name: market_trades_p2025102919_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX market_trades_p2025102919_trade_id ON public.market_trades_p2025102919 USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_trades_p2025102919_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102919_ts ON public.market_trades_p2025102919 USING btree (ts DESC);


--
-- Name: market_trades_p2025102920_tok_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102920_tok_ts ON public.market_trades_p2025102920 USING btree (token_id, ts DESC);


--
-- Name: market_trades_p2025102920_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX market_trades_p2025102920_trade_id ON public.market_trades_p2025102920 USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_trades_p2025102920_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_trades_p2025102920_ts ON public.market_trades_p2025102920 USING btree (ts DESC);


--
-- Name: uq_market_trades_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_market_trades_trade_id ON public.market_trades_old USING btree (trade_id) WHERE (trade_id IS NOT NULL);


--
-- Name: market_features_p20251018_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p20251018_pkey;


--
-- Name: market_features_p20251019_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p20251019_pkey;


--
-- Name: market_features_p20251020_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p20251020_pkey;


--
-- Name: market_features_p2025102911_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p2025102911_pkey;


--
-- Name: market_features_p2025102912_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p2025102912_pkey;


--
-- Name: market_features_p2025102913_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p2025102913_pkey;


--
-- Name: market_features_p2025102914_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p2025102914_pkey;


--
-- Name: market_features_p2025102915_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p2025102915_pkey;


--
-- Name: market_features_p2025102916_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p2025102916_pkey;


--
-- Name: market_features_p2025102917_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_features_pkey1 ATTACH PARTITION public.market_features_p2025102917_pkey;


--
-- Name: market_quotes_p2025102915_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_quotes_pkey1 ATTACH PARTITION public.market_quotes_p2025102915_pkey;


--
-- Name: market_quotes_p2025102916_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_quotes_pkey1 ATTACH PARTITION public.market_quotes_p2025102916_pkey;


--
-- Name: market_quotes_p2025102917_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_quotes_pkey1 ATTACH PARTITION public.market_quotes_p2025102917_pkey;


--
-- Name: market_quotes_p2025102918_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_quotes_pkey1 ATTACH PARTITION public.market_quotes_p2025102918_pkey;


--
-- Name: market_quotes_p2025102919_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_quotes_pkey1 ATTACH PARTITION public.market_quotes_p2025102919_pkey;


--
-- Name: market_quotes_p2025102920_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_quotes_pkey1 ATTACH PARTITION public.market_quotes_p2025102920_pkey;


--
-- Name: market_quotes_p2025102921_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_quotes_pkey1 ATTACH PARTITION public.market_quotes_p2025102921_pkey;


--
-- Name: market_trades_p2025102914_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_trades_pkey1 ATTACH PARTITION public.market_trades_p2025102914_pkey;


--
-- Name: market_trades_p2025102915_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_trades_pkey1 ATTACH PARTITION public.market_trades_p2025102915_pkey;


--
-- Name: market_trades_p2025102916_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_trades_pkey1 ATTACH PARTITION public.market_trades_p2025102916_pkey;


--
-- Name: market_trades_p2025102917_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_trades_pkey1 ATTACH PARTITION public.market_trades_p2025102917_pkey;


--
-- Name: market_trades_p2025102918_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_trades_pkey1 ATTACH PARTITION public.market_trades_p2025102918_pkey;


--
-- Name: market_trades_p2025102919_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_trades_pkey1 ATTACH PARTITION public.market_trades_p2025102919_pkey;


--
-- Name: market_trades_p2025102920_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.market_trades_pkey1 ATTACH PARTITION public.market_trades_p2025102920_pkey;


--
-- Name: market_signals market_signals_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_signals
    ADD CONSTRAINT market_signals_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.trading_sessions(id);


--
-- Name: paper_orders paper_orders_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_orders
    ADD CONSTRAINT paper_orders_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.trading_sessions(id);


--
-- Name: paper_orders paper_orders_signal_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_orders
    ADD CONSTRAINT paper_orders_signal_id_fkey FOREIGN KEY (signal_id) REFERENCES public.market_signals(id);


--
-- Name: paper_positions paper_positions_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_positions
    ADD CONSTRAINT paper_positions_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.trading_sessions(id);


--
-- Name: paper_trades paper_trades_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_trades
    ADD CONSTRAINT paper_trades_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.trading_sessions(id);


--
-- Name: trading_sessions trading_sessions_strategy_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trading_sessions
    ADD CONSTRAINT trading_sessions_strategy_id_fkey FOREIGN KEY (strategy_id) REFERENCES public.strategies(id);


--
-- PostgreSQL database dump complete
--

\unrestrict da9jZlejdYA5sDxldqbsbN5jSbxGex7ToiYwOd6NsObMiPIZjCVT1pssts15gTj

