-- +goose Up
BEGIN;

-- === market_quotes: top-of-book snapshots (thin but frequent) ===
CREATE TABLE IF NOT EXISTS market_quotes (
    id BIGSERIAL PRIMARY KEY,
    token_id VARCHAR(80) NOT NULL,
    ts TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    best_bid DOUBLE PRECISION,
    best_ask DOUBLE PRECISION,
    bid_size1 DOUBLE PRECISION,
    ask_size1 DOUBLE PRECISION,
    spread_bps DOUBLE PRECISION,
    mid DOUBLE PRECISION
);
CREATE INDEX IF NOT EXISTS idx_market_quotes_token_ts
    ON market_quotes (token_id, ts DESC);

-- === market_trades: trade prints (for signed flow, VWAP, backtests) ===
CREATE TABLE IF NOT EXISTS market_trades (
    id BIGSERIAL PRIMARY KEY,
    token_id VARCHAR(80) NOT NULL,
    ts TIMESTAMPTZ NOT NULL,
    price DOUBLE PRECISION NOT NULL,
    size DOUBLE PRECISION NOT NULL,
    aggressor TEXT CHECK (aggressor IN ('buy','sell')),
    trade_id TEXT
);
-- Keep idempotency when upstream provides a trade_id
CREATE UNIQUE INDEX IF NOT EXISTS uq_market_trades_trade_id
    ON market_trades (trade_id)
    WHERE trade_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_market_trades_token_ts
    ON market_trades (token_id, ts DESC);

-- === market_features: rolling/derived metrics (emit every few seconds) ===
CREATE TABLE IF NOT EXISTS market_features (
    token_id VARCHAR(80) NOT NULL,
    ts TIMESTAMPTZ NOT NULL,
    ret_1m DOUBLE PRECISION,
    ret_5m DOUBLE PRECISION,
    vol_1m DOUBLE PRECISION,
    avg_vol_5m DOUBLE PRECISION,
    sigma_5m DOUBLE PRECISION,
    zscore_5m DOUBLE PRECISION,
    imbalance_top DOUBLE PRECISION,
    spread_bps DOUBLE PRECISION,
    broke_high_15m BOOLEAN,
    broke_low_15m BOOLEAN,
    time_to_resolve_h DOUBLE PRECISION,
    signed_flow_1m DOUBLE PRECISION,
    PRIMARY KEY (token_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_market_features_token_ts
    ON market_features (token_id, ts DESC);

-- === Convert DECIMAL -> DOUBLE PRECISION for float-native access in Go ===
ALTER TABLE market_scans
    ALTER COLUMN last_price     TYPE DOUBLE PRECISION USING last_price::double precision,
    ALTER COLUMN last_volume    TYPE DOUBLE PRECISION USING last_volume::double precision,
    ALTER COLUMN liquidity      TYPE DOUBLE PRECISION USING liquidity::double precision,
    ALTER COLUMN price_24h_ago  TYPE DOUBLE PRECISION USING price_24h_ago::double precision,
    ALTER COLUMN volume_24h_ago TYPE DOUBLE PRECISION USING volume_24h_ago::double precision;

ALTER TABLE market_events
    ALTER COLUMN old_value TYPE DOUBLE PRECISION USING old_value::double precision,
    ALTER COLUMN new_value TYPE DOUBLE PRECISION USING new_value::double precision;

COMMIT;

-- +goose Down
BEGIN;

-- Revert type changes (round to original scales to avoid errors)
ALTER TABLE market_events
    ALTER COLUMN old_value TYPE NUMERIC(20,6) USING round(old_value::numeric, 6)::numeric(20,6),
    ALTER COLUMN new_value TYPE NUMERIC(20,6) USING round(new_value::numeric, 6)::numeric(20,6);

ALTER TABLE market_scans
    ALTER COLUMN last_price     TYPE NUMERIC(10,6) USING round(last_price::numeric, 6)::numeric(10,6),
    ALTER COLUMN last_volume    TYPE NUMERIC(20,2) USING round(last_volume::numeric, 2)::numeric(20,2),
    ALTER COLUMN liquidity      TYPE NUMERIC(20,2) USING round(liquidity::numeric, 2)::numeric(20,2),
    ALTER COLUMN price_24h_ago  TYPE NUMERIC(10,6) USING round(price_24h_ago::numeric, 6)::numeric(10,6),
    ALTER COLUMN volume_24h_ago TYPE NUMERIC(20,2) USING round(volume_24h_ago::numeric, 2)::numeric(20,2);

DROP TABLE IF EXISTS market_features;
DROP TABLE IF EXISTS market_trades;
DROP TABLE IF EXISTS market_quotes;

COMMIT;
