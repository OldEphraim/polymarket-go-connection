// utils/types/strategy.go
package types

import (
	"context"
	"sync"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/OldEphraim/polymarket-go-connection/db"
)

// Position represents a trading position
type Position struct {
	TokenID      string
	Market       string
	Shares       float64
	EntryPrice   float64
	CurrentPrice float64
	EntryTime    time.Time
	Metadata     map[string]interface{}
}

// BaseStrategy contains common fields for all strategies
type BaseStrategy struct {
	InitialBalance float64
	CurrentBalance float64
	Positions      map[string]*Position
	PMClient       *client.PolymarketClient
	WSClient       *client.WSClient
	Store          *db.Store
	SessionID      int32
	Ctx            context.Context
	Cancel         context.CancelFunc
	Mu             sync.Mutex
}

// ExitParams contains parameters for checking exit conditions
type ExitParams struct {
	CurrentPrice float64
	EntryPrice   float64
	EntryTime    time.Time
	Metadata     map[string]interface{}
}
