package strategy_persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type DB = sql.DB

type Session struct{ ID int64 }

// Ensure a strategy row and a single open trading session.
// initialBalance is only used when we need to create a new session.
func EnsureOpenSession(ctx context.Context, db *DB, strategyName string, initialBalance float64) (Session, error) {
	var strategyID int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO strategies (name, initial_balance)
		VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id
	`, strategyName, initialBalance).Scan(&strategyID)
	if err != nil {
		return Session{}, err
	}

	var sessionID sql.NullInt64
	err = db.QueryRowContext(ctx, `
		SELECT id
		FROM trading_sessions
		WHERE strategy_id = $1 AND ended_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1
	`, strategyID).Scan(&sessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Session{}, err
	}
	if sessionID.Valid {
		return Session{ID: sessionID.Int64}, nil
	}

	// Create a new open session
	err = db.QueryRowContext(ctx, `
		INSERT INTO trading_sessions (strategy_id, start_balance, current_balance, started_at, ended_at)
		VALUES ($1, $2, $2, NOW(), NULL)
		RETURNING id
	`, strategyID, initialBalance).Scan(&sessionID)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: sessionID.Int64}, nil
}

// ---- Cash helpers ----

func DebitSession(ctx context.Context, db *DB, sessionID int64, amount float64) error {
	if amount <= 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		UPDATE trading_sessions
		SET current_balance = current_balance - $1
		WHERE id = $2
	`, amount, sessionID)
	return err
}

func CreditSession(ctx context.Context, db *DB, sessionID int64, amount float64) error {
	if amount <= 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		UPDATE trading_sessions
		SET current_balance = current_balance + $1
		WHERE id = $2
	`, amount, sessionID)
	return err
}

// ---- Entries / exits ----

type EntryParams struct {
	SessionID int64
	TokenID   string
	Side      string // "YES"/"NO"
	Price     float64
	Size      float64 // SHARES (notional = shares * price)
	SignalID  *int64
	Reason    string
}

func RecordEntry(ctx context.Context, db *DB, p EntryParams) error {
	// 1) order row (filled immediately in paper)
	_, err := db.ExecContext(ctx, `
		INSERT INTO paper_orders (session_id, signal_id, token_id, side, price, size, status, created_at, filled_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'filled', NOW(), NOW())
	`, p.SessionID, p.SignalID, p.TokenID, p.Side, p.Price, p.Size)
	if err != nil {
		return err
	}

	// 2) upsert position (average price for additional adds)
	_, err = db.ExecContext(ctx, `
		INSERT INTO paper_positions (session_id, token_id, shares, avg_entry_price, current_price, unrealized_pnl, updated_at)
		VALUES ($1, $2, $3, $4, $4, 0.0, NOW())
		ON CONFLICT (session_id, token_id) DO UPDATE
		SET shares = paper_positions.shares + EXCLUDED.shares,
		    avg_entry_price = CASE
		      WHEN (paper_positions.shares + EXCLUDED.shares) = 0 THEN paper_positions.avg_entry_price
		      ELSE ((paper_positions.avg_entry_price * paper_positions.shares) + (EXCLUDED.avg_entry_price * EXCLUDED.shares))
		           / NULLIF(paper_positions.shares + EXCLUDED.shares, 0)
		    END,
		    current_price = EXCLUDED.current_price,
		    updated_at = NOW()
	`, p.SessionID, p.TokenID, p.Size, p.Price)
	if err != nil {
		return err
	}

	// 3) fills / trades table (no realized pnl on entry)
	_, err = db.ExecContext(ctx, `
		INSERT INTO paper_trades (session_id, token_id, side, price, shares, notional, realized_pnl, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 0.0, NOW())
	`, p.SessionID, p.TokenID, p.Side, p.Price, p.Size, p.Price*p.Size)
	return err
}

type ExitParams struct {
	SessionID int64
	TokenID   string
	ExitPrice float64
	ExitSize  float64 // if 0 => close all
	Reason    string
	SideHint  string // optional, "YES"/"NO" to compute realized correctly if needed
}

func RecordExit(ctx context.Context, db *DB, p ExitParams) error {
	// Load current position
	var shares, avgEntry sql.NullFloat64
	err := db.QueryRowContext(ctx, `
		SELECT shares, avg_entry_price
		FROM paper_positions
		WHERE session_id = $1 AND token_id = $2
	`, p.SessionID, p.TokenID).Scan(&shares, &avgEntry)
	if err != nil {
		return err
	}
	if !shares.Valid || shares.Float64 <= 0 {
		return nil // nothing to exit
	}
	closeShares := p.ExitSize
	if closeShares <= 0 || closeShares > shares.Float64 {
		closeShares = shares.Float64
	}

	// Try to infer side (optional)
	side := p.SideHint
	if side == "" {
		// Heuristic: look up the most recent entry trade side for this token+session
		_ = db.QueryRowContext(ctx, `
			SELECT side FROM paper_trades
			WHERE session_id = $1 AND token_id = $2
			ORDER BY created_at DESC
			LIMIT 1
		`, p.SessionID, p.TokenID).Scan(&side)
		if side == "" {
			side = "YES" // default if unknown
		}
	}

	// Realized PnL for the exited portion
	realized := (p.ExitPrice - avgEntry.Float64) * closeShares
	if side == "NO" {
		realized = (avgEntry.Float64 - p.ExitPrice) * closeShares
	}

	// 1) order row representing the exit (paper)
	_, err = db.ExecContext(ctx, `
		INSERT INTO paper_orders (session_id, token_id, side, price, size, status, created_at, filled_at)
		VALUES ($1, $2, 'EXIT', $3, $4, 'filled', NOW(), NOW())
	`, p.SessionID, p.TokenID, p.ExitPrice, closeShares)
	if err != nil {
		return err
	}

	// 2) reduce position
	_, err = db.ExecContext(ctx, `
		UPDATE paper_positions
		SET shares = shares - $3,
		    current_price = $4,
		    unrealized_pnl = (shares - $3) * ($4 - avg_entry_price),
		    updated_at = NOW()
		WHERE session_id = $1 AND token_id = $2
	`, p.SessionID, p.TokenID, closeShares, p.ExitPrice)
	if err != nil {
		return err
	}

	// 3) fill / trade row with realized pnl
	_, err = db.ExecContext(ctx, `
		INSERT INTO paper_trades (session_id, token_id, side, price, shares, notional, realized_pnl, created_at)
		VALUES ($1, $2, 'EXIT', $3, $4, $5, $6, NOW())
	`, p.SessionID, p.TokenID, p.ExitPrice, closeShares, p.ExitPrice*closeShares, realized)
	if err != nil {
		return err
	}

	// 4) accumulate realized pnl into the session
	_, err = db.ExecContext(ctx, `
		UPDATE trading_sessions
		SET realized_pnl = COALESCE(realized_pnl, 0) + $2
		WHERE id = $1
	`, p.SessionID, realized)
	return err
}

// Optional: call at shutdown to mark session ended.
func EndSession(ctx context.Context, db *DB, sessionID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE trading_sessions SET ended_at = $2 WHERE id = $1 AND ended_at IS NULL
	`, sessionID, time.Now())
	return err
}
