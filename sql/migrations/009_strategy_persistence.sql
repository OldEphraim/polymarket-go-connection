-- +goose Up
-- Create paper_trades table and add realized_pnl to trading_sessions

-- paper fills for realized PnL bookkeeping
CREATE TABLE IF NOT EXISTS paper_trades (
  id            BIGSERIAL PRIMARY KEY,
  session_id    INTEGER NOT NULL REFERENCES trading_sessions(id),
  token_id      VARCHAR(80) NOT NULL,
  side          VARCHAR(10) NOT NULL, -- 'YES'/'NO' for entries, 'EXIT' for exits
  price         NUMERIC(10,6) NOT NULL,
  shares        NUMERIC(15,6) NOT NULL,
  notional      NUMERIC(15,6) NOT NULL,
  realized_pnl  NUMERIC(15,6) NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_paper_trades_session_time ON paper_trades(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_paper_trades_token_time   ON paper_trades(token_id, created_at DESC);

-- realized pnl column on sessions
ALTER TABLE trading_sessions
  ADD COLUMN IF NOT EXISTS realized_pnl NUMERIC(15,6) NOT NULL DEFAULT 0;

-- +goose Down
-- Drop new column and table (safe if empty; mind data loss if reverting)
ALTER TABLE trading_sessions
  DROP COLUMN IF EXISTS realized_pnl;

DROP INDEX IF EXISTS idx_paper_trades_token_time;
DROP INDEX IF EXISTS idx_paper_trades_session_time;
DROP TABLE IF EXISTS paper_trades;