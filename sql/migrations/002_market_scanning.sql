-- +goose Up
-- Table for tracking all market scans
CREATE TABLE market_scans (
    id SERIAL PRIMARY KEY,
    token_id VARCHAR(80) UNIQUE NOT NULL,
    event_id VARCHAR(255),
    slug VARCHAR(255),
    question TEXT,
    last_price DECIMAL(10, 6),
    last_volume DECIMAL(20, 2),
    liquidity DECIMAL(20, 2),
    last_scanned_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    price_24h_ago DECIMAL(10, 6),
    volume_24h_ago DECIMAL(20, 2),
    scan_count BIGINT DEFAULT 0,
    is_active BOOLEAN DEFAULT true,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Table for tracking market events detected by gatherer
CREATE TABLE market_events (
    id SERIAL PRIMARY KEY,
    token_id VARCHAR(80) NOT NULL,
    event_type VARCHAR(50),
    old_value DECIMAL(20, 6),
    new_value DECIMAL(20, 6),
    metadata JSONB,
    detected_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX idx_active_market_scans ON market_scans(is_active, last_scanned_at);
CREATE INDEX idx_market_scans_volume ON market_scans(last_volume DESC) WHERE is_active = true;
CREATE INDEX idx_market_scans_price_change ON market_scans((last_price - price_24h_ago)) WHERE is_active = true;
CREATE INDEX idx_market_events_type ON market_events(event_type, detected_at DESC);
CREATE INDEX idx_market_events_token ON market_events(token_id, detected_at DESC);

-- +goose Down
DROP TABLE IF EXISTS market_events;
DROP TABLE IF EXISTS market_scans;