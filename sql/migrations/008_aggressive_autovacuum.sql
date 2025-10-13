-- +goose Up
-- Make autovacuum more aggressive for high-churn tables
-- These tables get millions of inserts and deletes daily
ALTER TABLE market_features SET (
    autovacuum_vacuum_scale_factor = 0.05,  -- vacuum when 5% dead (default 20%)
    autovacuum_analyze_scale_factor = 0.02   -- analyze when 2% changed (default 10%)
);

ALTER TABLE market_trades SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

ALTER TABLE market_quotes SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

-- +goose Down
-- Reset to PostgreSQL defaults
ALTER TABLE market_features RESET (
    autovacuum_vacuum_scale_factor,
    autovacuum_analyze_scale_factor
);

ALTER TABLE market_trades RESET (
    autovacuum_vacuum_scale_factor,
    autovacuum_analyze_scale_factor
);

ALTER TABLE market_quotes RESET (
    autovacuum_vacuum_scale_factor,
    autovacuum_analyze_scale_factor
);
