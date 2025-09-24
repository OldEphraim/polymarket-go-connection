package strategy

import (
	"context"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/utils/common"
	"github.com/OldEphraim/polymarket-go-connection/utils/dbops"
	"github.com/OldEphraim/polymarket-go-connection/utils/logging"
	"github.com/OldEphraim/polymarket-go-connection/utils/market"
	"github.com/OldEphraim/polymarket-go-connection/utils/position"
	"github.com/OldEphraim/polymarket-go-connection/utils/types"
)

// ExitChecker handles common exit checking logic
type ExitChecker struct {
	PosManager    *position.Manager
	MarketFetcher market.Fetcher
	Logger        *logging.StrategyLogger
	PerfTracker   *logging.PerformanceTracker
	Store         *db.Store // CHANGED: needs to be a pointer
	SessionID     int32     // CHANGED: needs to be int32
	Ctx           context.Context
}

// CustomExitFunc defines strategy-specific exit logic
type CustomExitFunc func(pos *types.Position, params types.ExitParams) (bool, string)

// CheckExitConditions checks all positions for exit conditions
func (ec *ExitChecker) CheckExitConditions(
	positions map[string]*types.Position,
	currentBalance *float64,
	customCheck CustomExitFunc,
) {
	for key, pos := range positions {
		// Update price from orderbook
		book, err := ec.MarketFetcher.GetOrderBook(pos.TokenID)
		if err == nil {
			if bestBid := common.ExtractBestBid(book); bestBid > 0 {
				// Sanity check: ignore extreme price movements
				priceChange := (bestBid - pos.CurrentPrice) / pos.CurrentPrice
				if priceChange > -0.50 && priceChange < 0.50 {
					pos.CurrentPrice = bestBid
				}
			}
		}

		// Build exit parameters
		params := types.ExitParams{
			CurrentPrice: pos.CurrentPrice,
			EntryPrice:   pos.EntryPrice,
			EntryTime:    pos.EntryTime,
			Metadata:     pos.Metadata,
		}

		// Check standard exit conditions
		shouldExit, reason := ec.StandardExitCheck(pos, params, customCheck)

		if shouldExit {
			ec.ExecuteExit(key, pos, currentBalance, reason, positions)
		}
	}
}

// StandardExitCheck performs standard exit checks plus custom strategy logic
func (ec *ExitChecker) StandardExitCheck(
	pos *types.Position,
	params types.ExitParams,
	customCheck CustomExitFunc,
) (bool, string) {
	// First check standard conditions (stop loss, take profit)
	shouldExit, reason := ec.PosManager.CheckExitConditions(params)
	if shouldExit {
		return true, reason
	}

	// Check custom strategy-specific conditions
	if customCheck != nil {
		shouldExit, reason = customCheck(pos, params)
		if shouldExit {
			return true, reason
		}
	}

	// Standard data collection time limit
	holdTime := time.Since(pos.EntryTime)
	if holdTime > 4*time.Hour {
		return true, "Max hold time (4h) for data collection"
	}

	return false, ""
}

// ExecuteExit handles the exit of a position
func (ec *ExitChecker) ExecuteExit(
	key string,
	pos *types.Position,
	currentBalance *float64,
	reason string,
	positions map[string]*types.Position,
) {
	if pos == nil {
		return
	}

	// Calculate proceeds and P&L
	var ourSize float64
	if metadata, ok := pos.Metadata["ourSize"]; ok {
		ourSize = metadata.(float64)
	} else {
		// Fall back to calculating from shares and entry price
		ourSize = pos.Shares * pos.EntryPrice
	}

	proceeds := pos.Shares * pos.CurrentPrice
	pnl, _ := ec.PosManager.CalculatePnLFromCost(ourSize, proceeds)

	// Log the exit
	ec.Logger.LogExit(pos.TokenID, pos.EntryPrice, pos.CurrentPrice, pnl, reason)
	ec.PerfTracker.RecordTrade(pnl)

	// Record signal in database
	dbops.RecordSignal(ec.Ctx, ec.Store, dbops.SignalParams{
		SessionID:    ec.SessionID,
		TokenID:      pos.TokenID,
		SignalType:   "sell",
		BestBid:      pos.CurrentPrice,
		BestAsk:      pos.CurrentPrice,
		ActionReason: reason,
		Confidence:   100,
	})

	// Update balance and remove position
	delete(positions, key)
	*currentBalance += proceeds
}

// ExitAllPositions exits all positions (used during shutdown)
func (ec *ExitChecker) ExitAllPositions(
	positions map[string]*types.Position,
	currentBalance *float64,
	reason string,
) {
	for key, pos := range positions {
		price := pos.CurrentPrice
		if price == 0 {
			price = pos.EntryPrice
		}
		pos.CurrentPrice = price
		ec.ExecuteExit(key, pos, currentBalance, reason, positions)
	}
}
