package dbops

import (
	"context"
	dbsql "database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

// StrategyConfig holds common configuration for strategies
type StrategyConfig struct {
	Name           string
	Config         json.RawMessage
	InitialBalance float64
}

// Add this function near the top of each main.go file
func GetDBConnection() string {
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		return dbURL
	}
	return "user=a8garber dbname=polymarket_dev sslmode=disable"
}

// InitializeStrategy creates a new strategy and session in the database
// Returns the strategy ID, session ID, and any error
func InitializeStrategy(ctx context.Context, store *db.Store, config StrategyConfig) (strategyID int32, sessionID int32, err error) {
	// Create strategy record
	strategyRec, err := store.CreateStrategy(ctx, database.CreateStrategyParams{
		Name:           config.Name,
		Config:         config.Config,
		InitialBalance: dbsql.NullString{String: fmt.Sprintf("%.2f", config.InitialBalance), Valid: true},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create strategy: %w", err)
	}

	// Create session record
	session, err := store.CreateSession(ctx, database.CreateSessionParams{
		StrategyID:     dbsql.NullInt32{Int32: strategyRec.ID, Valid: true},
		StartBalance:   dbsql.NullString{String: fmt.Sprintf("%.2f", config.InitialBalance), Valid: true},
		CurrentBalance: dbsql.NullString{String: fmt.Sprintf("%.2f", config.InitialBalance), Valid: true},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create session: %w", err)
	}

	return strategyRec.ID, session.ID, nil
}

// SignalParams holds parameters for recording a signal
type SignalParams struct {
	SessionID    int32
	TokenID      string
	SignalType   string // "buy" or "sell"
	BestBid      float64
	BestAsk      float64
	ActionReason string
	Confidence   float64
}

// RecordSignal records a buy or sell signal in the database
// Returns the signal ID if successful
func RecordSignal(ctx context.Context, store *db.Store, params SignalParams) (signalID int32, err error) {
	signal, err := store.RecordSignal(ctx, database.RecordSignalParams{
		SessionID:    dbsql.NullInt32{Int32: params.SessionID, Valid: true},
		TokenID:      params.TokenID,
		SignalType:   params.SignalType,
		BestBid:      dbsql.NullString{String: fmt.Sprintf("%.6f", params.BestBid), Valid: true},
		BestAsk:      dbsql.NullString{String: fmt.Sprintf("%.6f", params.BestAsk), Valid: true},
		ActionReason: dbsql.NullString{String: params.ActionReason, Valid: true},
		Confidence:   dbsql.NullString{String: fmt.Sprintf("%.0f", params.Confidence), Valid: true},
	})

	if err != nil {
		return 0, fmt.Errorf("failed to record %s signal: %w", params.SignalType, err)
	}

	return signal.ID, nil
}

// UpdateSessionBalance updates the current balance for a session
func UpdateSessionBalance(ctx context.Context, store *db.Store, sessionID int32, balance float64) error {
	err := store.UpdateSessionBalance(ctx, database.UpdateSessionBalanceParams{
		ID:             sessionID,
		CurrentBalance: dbsql.NullString{String: fmt.Sprintf("%.2f", balance), Valid: true},
	})

	if err != nil {
		return fmt.Errorf("failed to update session balance: %w", err)
	}

	return nil
}

// EndSession marks a session as ended in the database
func EndSession(ctx context.Context, store *db.Store, sessionID int32) error {
	err := store.EndSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to end session: %w", err)
	}
	return nil
}
