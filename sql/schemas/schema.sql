-- Current schema state for polymarket_dev
-- This file should be kept in sync with migrations
-- Last updated: Migration 001

CREATE TABLE markets (
    id SERIAL PRIMARY KEY,
    token_id VARCHAR(80) UNIQUE NOT NULL,
    slug VARCHAR(255),
    question TEXT,
    outcome VARCHAR(50),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE strategies (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) UNIQUE NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    initial_balance DECIMAL(15, 6) DEFAULT 1000.00,
    active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE trading_sessions (
    id SERIAL PRIMARY KEY,
    strategy_id INTEGER REFERENCES strategies(id),
    start_balance DECIMAL(15, 6),
    current_balance DECIMAL(15, 6),
    started_at TIMESTAMP DEFAULT NOW(),
    ended_at TIMESTAMP
);

CREATE TABLE market_signals (
    id SERIAL PRIMARY KEY,
    session_id INTEGER REFERENCES trading_sessions(id),
    token_id VARCHAR(80) NOT NULL,
    signal_type VARCHAR(50) NOT NULL,
    timestamp TIMESTAMP DEFAULT NOW(),
    best_bid DECIMAL(10, 6),
    best_ask DECIMAL(10, 6),
    bid_liquidity DECIMAL(15, 6),
    ask_liquidity DECIMAL(15, 6),
    action_reason TEXT,
    confidence DECIMAL(5, 2)
);

CREATE TABLE paper_orders (
    id SERIAL PRIMARY KEY,
    session_id INTEGER REFERENCES trading_sessions(id),
    signal_id INTEGER REFERENCES market_signals(id),
    token_id VARCHAR(80) NOT NULL,
    side VARCHAR(10) NOT NULL,
    price DECIMAL(10, 6) NOT NULL,
    size DECIMAL(15, 6) NOT NULL,
    status VARCHAR(20) DEFAULT 'open',
    created_at TIMESTAMP DEFAULT NOW(),
    filled_at TIMESTAMP
);

CREATE TABLE paper_positions (
    id SERIAL PRIMARY KEY,
    session_id INTEGER REFERENCES trading_sessions(id),
    token_id VARCHAR(80) NOT NULL,
    shares DECIMAL(15, 6) NOT NULL,
    avg_entry_price DECIMAL(10, 6),
    current_price DECIMAL(10, 6),
    unrealized_pnl DECIMAL(15, 6),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(session_id, token_id)
);

-- Indexes
CREATE INDEX idx_session_time ON market_signals(session_id, timestamp);
CREATE INDEX idx_token_time ON market_signals(token_id, timestamp);
CREATE INDEX idx_session_status ON paper_orders(session_id, status);