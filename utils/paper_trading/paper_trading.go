package paper_trading

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
)

type Config struct {
	UnlimitedFunds bool    `json:"unlimited_funds"`
	InitialBalance float64 `json:"initial_balance"`
	TrackMetrics   bool    `json:"track_metrics"`
}

type Framework struct {
	store  *db.Store
	config Config
	mu     sync.RWMutex

	// Tracking
	positions map[string]*Position
	balance   float64
	totalPnL  float64
}

type Position struct {
	ID           int64
	TokenID      string
	Market       string
	Side         string
	Size         float64
	EntryPrice   float64
	CurrentPrice float64
	EntryTime    time.Time
	ExitTime     *time.Time
	ExitPrice    *float64
	PnL          float64
	Status       string // "open", "closed"
	Strategy     string
	EntryReason  string
	ExitReason   string
}

type Entry struct {
	TokenID  string
	Market   string
	Side     string
	Size     float64
	Price    float64
	Reason   string
	Strategy string
	Metadata map[string]interface{}
}

func New(store *db.Store, config Config) *Framework {
	return &Framework{
		store:     store,
		config:    config,
		positions: make(map[string]*Position),
		balance:   config.InitialBalance,
	}
}

// EnterPosition creates a new paper trading position
func (f *Framework) EnterPosition(ctx context.Context, entry Entry) (*Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Check if we already have a position in this market
	if existing, exists := f.positions[entry.TokenID]; exists && existing.Status == "open" {
		return nil, fmt.Errorf("already have open position in %s", entry.TokenID)
	}

	// Check balance if not unlimited
	cost := entry.Size * entry.Price
	if !f.config.UnlimitedFunds && cost > f.balance {
		return nil, fmt.Errorf("insufficient balance: need %.2f, have %.2f", cost, f.balance)
	}

	// Create position
	position := &Position{
		TokenID:      entry.TokenID,
		Market:       entry.Market,
		Side:         entry.Side,
		Size:         entry.Size,
		EntryPrice:   entry.Price,
		CurrentPrice: entry.Price,
		EntryTime:    time.Now(),
		Status:       "open",
		Strategy:     entry.Strategy,
		EntryReason:  entry.Reason,
	}

	// Deduct from balance if not unlimited
	if !f.config.UnlimitedFunds {
		f.balance -= cost
	}

	// Store in memory
	f.positions[entry.TokenID] = position

	// Record in database if tracking
	if f.config.TrackMetrics {
		// This would use your existing database methods
		// You might need to add a paper_positions table
	}

	return position, nil
}

// ExitPosition closes a paper trading position
func (f *Framework) ExitPosition(ctx context.Context, tokenID string, exitPrice float64, reason string) (*Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	position, exists := f.positions[tokenID]
	if !exists {
		return nil, fmt.Errorf("no position found for %s", tokenID)
	}

	if position.Status != "open" {
		return nil, fmt.Errorf("position already closed")
	}

	// Calculate P&L
	var pnl float64
	if position.Side == "YES" {
		pnl = (exitPrice - position.EntryPrice) * position.Size
	} else {
		pnl = (position.EntryPrice - exitPrice) * position.Size
	}

	// Update position
	now := time.Now()
	position.ExitTime = &now
	position.ExitPrice = &exitPrice
	position.PnL = pnl
	position.Status = "closed"
	position.ExitReason = reason

	// Update balance
	if !f.config.UnlimitedFunds {
		f.balance += position.Size * exitPrice
	}
	f.totalPnL += pnl

	// Record metrics if tracking
	if f.config.TrackMetrics {
		// Record to database
	}

	return position, nil
}

// UpdatePrice updates the current price for P&L tracking
func (f *Framework) UpdatePrice(tokenID string, newPrice float64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if position, exists := f.positions[tokenID]; exists && position.Status == "open" {
		position.CurrentPrice = newPrice
	}
}

// GetOpenPositions returns all open positions
func (f *Framework) GetOpenPositions() []*Position {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var open []*Position
	for _, pos := range f.positions {
		if pos.Status == "open" {
			open = append(open, pos)
		}
	}
	return open
}

// GetMetrics returns current trading metrics
func (f *Framework) GetMetrics() map[string]interface{} {
	f.mu.RLock()
	defer f.mu.RUnlock()

	openCount := 0
	closedCount := 0
	winCount := 0
	totalTrades := 0

	for _, pos := range f.positions {
		if pos.Status == "open" {
			openCount++
		} else {
			closedCount++
			totalTrades++
			if pos.PnL > 0 {
				winCount++
			}
		}
	}

	winRate := float64(0)
	if totalTrades > 0 {
		winRate = float64(winCount) / float64(totalTrades)
	}

	return map[string]interface{}{
		"open_positions":  openCount,
		"closed_trades":   closedCount,
		"total_pnl":       f.totalPnL,
		"win_rate":        winRate,
		"current_balance": f.balance,
	}
}
