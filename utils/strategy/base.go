package strategy

import (
	"context"
	"sync"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/OldEphraim/polymarket-go-connection/db"
)

type BaseStrategy struct {
	Store          *db.Store
	PMClient       *client.PolymarketClient
	WSClient       *client.WSClient
	SessionID      int32
	InitialBalance float64
	CurrentBalance float64
	Positions      map[string]*Position
	Mu             sync.Mutex
	Ctx            context.Context
	Cancel         context.CancelFunc
}

type Position struct {
	TokenID      string
	Market       string
	Shares       float64
	EntryPrice   float64
	CurrentPrice float64
	EntryTime    time.Time
	Metadata     map[string]interface{} // For strategy-specific data
}
