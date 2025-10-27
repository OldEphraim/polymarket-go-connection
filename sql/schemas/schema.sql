--
-- PostgreSQL database dump
--

\restrict NjBaMTzlKhA0fYpfWI12mwsgERbygVzuvp559dpQ9dbcNZSdEFEikGSZOS45hIr

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
-- Name: create_market_features_partition(date); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.create_market_features_partition(p_date date) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
  part_name text := format('market_features_p%s', to_char(p_date, 'YYYYMMDD'));
  start_ts  timestamptz := date_trunc('day', p_date)::timestamptz;
  end_ts    timestamptz := (date_trunc('day', p_date) + interval '1 day')::timestamptz;

  idx_ts    text := part_name || '_ts';
  idx_tokts text := part_name || '_tok_ts';
  idx_brin  text := part_name || '_brin_ts';
BEGIN
  EXECUTE format(
    'CREATE TABLE IF NOT EXISTS %I PARTITION OF market_features
       FOR VALUES FROM (%L) TO (%L)
       WITH (autovacuum_vacuum_scale_factor=0.05,
             autovacuum_analyze_scale_factor=0.02)',
    part_name, start_ts, end_ts
  );

  -- Local indexes mirroring old access paths
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (ts DESC)',                       idx_ts,    part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I (token_id, ts DESC)',              idx_tokts, part_name);
  EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I USING brin (ts) WITH (pages_per_range=64)', idx_brin, part_name);
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
-- Name: drop_archived_market_features_partitions(integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.drop_archived_market_features_partitions(p_keep_days integer) RETURNS integer
    LANGUAGE plpgsql
    AS $$
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
$$;


--
-- Name: ensure_features_partitions(integer, integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.ensure_features_partitions(p_days_back integer DEFAULT 1, p_days_forward integer DEFAULT 1) RETURNS void
    LANGUAGE plpgsql
    AS $$
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
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


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
    CONSTRAINT market_trades_aggressor_check CHECK ((aggressor = ANY (ARRAY['buy'::text, 'sell'::text])))
)
WITH (autovacuum_vacuum_scale_factor='0.05', autovacuum_analyze_scale_factor='0.02');


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
-- Name: market_features_old market_features_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_features_old
    ADD CONSTRAINT market_features_pkey PRIMARY KEY (token_id, ts);


--
-- Name: market_quotes market_quotes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_quotes
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
-- Name: market_trades market_trades_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_trades
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

CREATE INDEX brin_market_quotes_ts ON public.market_quotes USING brin (ts) WITH (pages_per_range='64');


--
-- Name: brin_market_trades_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX brin_market_trades_ts ON public.market_trades USING brin (ts) WITH (pages_per_range='64');


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

CREATE INDEX idx_market_quotes_token_ts ON public.market_quotes USING btree (token_id, ts DESC);


--
-- Name: idx_market_quotes_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_quotes_ts ON public.market_quotes USING btree (ts DESC);


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

CREATE INDEX idx_market_trades_token_ts ON public.market_trades USING btree (token_id, ts DESC);


--
-- Name: idx_market_trades_ts; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_market_trades_ts ON public.market_trades USING btree (ts DESC);


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
-- Name: uq_market_trades_trade_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_market_trades_trade_id ON public.market_trades USING btree (trade_id) WHERE (trade_id IS NOT NULL);


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

\unrestrict NjBaMTzlKhA0fYpfWI12mwsgERbygVzuvp559dpQ9dbcNZSdEFEikGSZOS45hIr

