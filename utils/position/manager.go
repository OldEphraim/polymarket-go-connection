package position

import (
	"fmt"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/utils/types"
)

// Manager handles position entry/exit logic and P&L calculations
type Manager struct {
	MaxPositions      int
	MaxPositionSize   float64
	StopLossPercent   float64
	TakeProfitPercent float64
	MaxHoldTime       time.Duration
}

// NewManager creates a new position manager with the given parameters
func NewManager(maxPositions int, maxPositionSize, stopLoss, takeProfit float64, maxHoldTime time.Duration) *Manager {
	return &Manager{
		MaxPositions:      maxPositions,
		MaxPositionSize:   maxPositionSize,
		StopLossPercent:   stopLoss,
		TakeProfitPercent: takeProfit,
		MaxHoldTime:       maxHoldTime,
	}
}

// CanEnter checks if we can enter a new position
func (m *Manager) CanEnter(balance float64, numPositions int, requiredSize float64) bool {
	if numPositions >= m.MaxPositions {
		return false
	}

	if requiredSize > m.MaxPositionSize {
		requiredSize = m.MaxPositionSize
	}

	return balance >= requiredSize
}

// CalculatePositionSize determines the appropriate position size
func (m *Manager) CalculatePositionSize(desiredSize float64, minSize float64) float64 {
	if desiredSize > m.MaxPositionSize {
		return m.MaxPositionSize
	}
	if desiredSize < minSize {
		return minSize
	}
	return desiredSize
}

// CalculatePnL calculates profit/loss for a position
func (m *Manager) CalculatePnL(entryPrice, currentPrice, shares float64) (pnl float64, pnlPercent float64) {
	positionValue := shares * entryPrice
	currentValue := shares * currentPrice
	pnl = currentValue - positionValue
	pnlPercent = (currentPrice - entryPrice) / entryPrice
	return pnl, pnlPercent
}

// CalculatePnLFromCost calculates P&L based on initial cost
func (m *Manager) CalculatePnLFromCost(cost, proceeds float64) (pnl float64, pnlPercent float64) {
	pnl = proceeds - cost
	pnlPercent = pnl / cost
	return pnl, pnlPercent
}

// CheckExitConditions checks if a position should be exited
func (m *Manager) CheckExitConditions(params types.ExitParams) (shouldExit bool, reason string) {
	// Calculate P&L percentage
	pnlPercent := (params.CurrentPrice - params.EntryPrice) / params.EntryPrice
	holdTime := time.Since(params.EntryTime)

	// Check stop loss
	if pnlPercent <= -m.StopLossPercent {
		return true, fmt.Sprintf("Stop loss (%.1f%%)", pnlPercent*100)
	}

	// Check take profit
	if pnlPercent >= m.TakeProfitPercent {
		return true, fmt.Sprintf("Take profit (%.1f%%)", pnlPercent*100)
	}

	// Check max hold time
	if m.MaxHoldTime > 0 && holdTime > m.MaxHoldTime {
		return true, fmt.Sprintf("Max hold time (%.0f hours)", holdTime.Hours())
	}

	return false, ""
}

// CheckExitWithCustomRules allows strategies to add custom exit rules
func (m *Manager) CheckExitWithCustomRules(params types.ExitParams, customCheck func(types.ExitParams) (bool, string)) (shouldExit bool, reason string) {
	// First check standard conditions
	shouldExit, reason = m.CheckExitConditions(params)
	if shouldExit {
		return shouldExit, reason
	}

	// Then check custom conditions
	return customCheck(params)
}

// PositionSummary provides a summary of a position for logging
type PositionSummary struct {
	TokenID      string
	Market       string
	EntryPrice   float64
	CurrentPrice float64
	Shares       float64
	PnL          float64
	PnLPercent   float64
	HoldTime     time.Duration
}

// GetPositionSummary creates a summary of a position
func (m *Manager) GetPositionSummary(pos *types.Position) PositionSummary {
	pnl, pnlPercent := m.CalculatePnL(pos.EntryPrice, pos.CurrentPrice, pos.Shares)

	return PositionSummary{
		TokenID:      pos.TokenID,
		Market:       pos.Market,
		EntryPrice:   pos.EntryPrice,
		CurrentPrice: pos.CurrentPrice,
		Shares:       pos.Shares,
		PnL:          pnl,
		PnLPercent:   pnlPercent,
		HoldTime:     time.Since(pos.EntryTime),
	}
}
