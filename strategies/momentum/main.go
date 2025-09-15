package main

import (
	"context"
	dbsql "database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	"github.com/joho/godotenv"
)

type Strategy struct {
	store     *db.Store
	pmClient  *client.PolymarketClient
	wsClient  *client.WSClient
	sessionID int32

	// Strategy parameters
	initialBalance    float64
	currentBalance    float64
	maxPositionSize   float64
	stopLossPercent   float64
	takeProfitPercent float64

	// Position tracking
	positions map[string]*Position
	mu        sync.Mutex

	// Control
	ctx    context.Context
	cancel context.CancelFunc
}

type Position struct {
	TokenID      string
	Market       string
	Shares       float64
	EntryPrice   float64
	CurrentPrice float64
	EntryTime    time.Time
	UpdateChan   <-chan client.MarketUpdate
}

func main() {
	godotenv.Load()

	// SHORT TEST DURATION - change to 8*time.Hour for overnight
	duration := 10 * time.Minute
	initialBalance := 1000.0

	log.Printf("Starting momentum strategy for %v with $%.2f", duration, initialBalance)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	strategy, err := NewStrategy(ctx, cancel, initialBalance)
	if err != nil {
		log.Fatal(err)
	}

	strategy.Run()
}

func NewStrategy(ctx context.Context, cancel context.CancelFunc, balance float64) (*Strategy, error) {
	store, err := db.NewStore("user=a8garber dbname=polymarket_dev sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	strategyRec, err := store.CreateStrategy(ctx, database.CreateStrategyParams{
		Name: "momentum_overnight",
		Config: json.RawMessage(`{
            "max_position_size": 100,
            "stop_loss": 0.15,
            "take_profit": 0.25
        }`),
		InitialBalance: dbsql.NullString{String: fmt.Sprintf("%.2f", balance), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create strategy: %w", err)
	}

	session, err := store.CreateSession(ctx, database.CreateSessionParams{
		StrategyID:     dbsql.NullInt32{Int32: strategyRec.ID, Valid: true},
		StartBalance:   dbsql.NullString{String: fmt.Sprintf("%.2f", balance), Valid: true},
		CurrentBalance: dbsql.NullString{String: fmt.Sprintf("%.2f", balance), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	pmClient, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		return nil, fmt.Errorf("failed to create Polymarket client: %w", err)
	}

	wsClient, err := client.NewWSClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create WebSocket client: %w", err)
	}

	log.Printf("Created session %d for strategy %s", session.ID, strategyRec.Name)

	return &Strategy{
		store:             store,
		pmClient:          pmClient,
		wsClient:          wsClient,
		sessionID:         session.ID,
		initialBalance:    balance,
		currentBalance:    balance,
		maxPositionSize:   100.0,
		stopLossPercent:   0.15,
		takeProfitPercent: 0.25,
		positions:         make(map[string]*Position),
		ctx:               ctx,
		cancel:            cancel,
	}, nil
}

func (s *Strategy) Run() {
	log.Println("Starting strategy components...")

	go s.wsClient.Listen(s.ctx)
	go s.discoverMarkets()
	go s.monitorPositions()
	go s.statusUpdates()

	<-s.ctx.Done()
	s.shutdown()
}

func (s *Strategy) discoverMarkets() {
	// Search immediately, then every 30 minutes
	s.searchAndSubscribe()

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.searchAndSubscribe()
		}
	}
}

func (s *Strategy) searchAndSubscribe() {
	queries := []string{"NFL", "NBA", "Premier League", "bitcoin"}

	log.Printf("Searching for markets...")

	for _, query := range queries {
		markets := s.searchMarkets(query)
		log.Printf("Found %d markets for query '%s'", len(markets), query)

		for _, market := range markets {
			if !market.Active || market.Volume24h < 1000 {
				continue
			}

			for _, token := range market.Tokens {
				s.mu.Lock()
				if _, exists := s.positions[token.TokenID]; exists {
					s.mu.Unlock()
					continue
				}
				s.mu.Unlock()

				if token.Price < 0.20 || token.Price > 0.80 {
					continue
				}

				updates, err := s.wsClient.Subscribe(token.TokenID)
				if err != nil {
					log.Printf("Failed to subscribe to %s: %v", token.TokenID[:20], err)
					continue
				}

				go s.monitorToken(token, market.Question, updates)

				// Safe market name truncation
				marketName := market.Question
				if len(marketName) > 50 {
					marketName = marketName[:47] + "..."
				}
				log.Printf("Monitoring: %s (%.1f%%)", marketName, token.Price*100)
			}
		}
	}
}

func (s *Strategy) searchMarkets(query string) []MarketInfo {
	cmd := exec.Command("./run_search.sh", query, "--json", "--limit", "5")

	output, err := cmd.Output()
	if err != nil {
		log.Printf("Search failed for '%s': %v", query, err)
		return []MarketInfo{}
	}

	// Handle outcome_prices as strings
	var result struct {
		Markets []struct {
			Question      string   `json:"question"`
			Active        bool     `json:"active"`
			Volume24hr    float64  `json:"volume24hr"`
			TokenIDs      []string `json:"token_ids"`
			Outcomes      []string `json:"outcomes"`
			OutcomePrices []string `json:"outcome_prices"` // These come as strings!
		} `json:"markets"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		log.Printf("Failed to parse search results: %v", err)
		return []MarketInfo{}
	}

	var markets []MarketInfo
	for _, m := range result.Markets {
		market := MarketInfo{
			Question:  m.Question,
			Active:    m.Active,
			Volume24h: m.Volume24hr,
		}

		// Convert string prices to floats
		for i := 0; i < len(m.TokenIDs) && i < len(m.Outcomes) && i < len(m.OutcomePrices); i++ {
			price, err := strconv.ParseFloat(m.OutcomePrices[i], 64)
			if err != nil {
				price = 0 // Default if parse fails
			}

			market.Tokens = append(market.Tokens, TokenInfo{
				TokenID: m.TokenIDs[i],
				Outcome: m.Outcomes[i],
				Price:   price,
			})
		}

		markets = append(markets, market)
	}

	return markets
}

func (s *Strategy) monitorToken(token TokenInfo, market string, updates <-chan client.MarketUpdate) {
	entryThreshold := 0.001
	var lastPrice float64
	updateCount := 0

	for {
		select {
		case <-s.ctx.Done():
			log.Printf("Token monitor shutting down for %s after %d updates",
				token.TokenID[:20], updateCount)
			return

		case update := <-updates:
			updateCount++
			if updateCount <= 5 {
				log.Printf("Update #%d for %s: Type=%s, Ask=%.4f",
					updateCount, token.TokenID[:20], update.EventType, update.BestAsk)
			}

			if update.EventType != "book" {
				continue
			}

			currentPrice := update.BestAsk
			if currentPrice == 0 {
				continue
			}

			if lastPrice > 0 {
				priceChange := (currentPrice - lastPrice) / lastPrice

				if updateCount%10 == 0 {
					log.Printf("Price change for %s: %.4f%% (%.4f -> %.4f)",
						token.TokenID[:20], priceChange*100, lastPrice, currentPrice)
				}

				s.mu.Lock()
				_, hasPosition := s.positions[token.TokenID]
				canAfford := s.currentBalance >= s.maxPositionSize
				s.mu.Unlock()

				if !hasPosition && canAfford && priceChange > entryThreshold {
					log.Printf("ENTRY SIGNAL: %s moved %.2f%% (threshold %.2f%%)",
						token.TokenID[:20], priceChange*100, entryThreshold*100)
					s.enterPosition(token.TokenID, market, currentPrice, updates)
				}
			}

			lastPrice = currentPrice
		}
	}
}

func (s *Strategy) enterPosition(tokenID, market string, price float64, updates <-chan client.MarketUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentBalance < s.maxPositionSize {
		return
	}

	shares := s.maxPositionSize / price

	_, err := s.store.RecordSignal(s.ctx, database.RecordSignalParams{
		SessionID:    dbsql.NullInt32{Int32: s.sessionID, Valid: true},
		TokenID:      tokenID,
		SignalType:   "buy",
		BestBid:      dbsql.NullString{String: fmt.Sprintf("%.6f", price), Valid: true},
		BestAsk:      dbsql.NullString{String: fmt.Sprintf("%.6f", price), Valid: true},
		ActionReason: dbsql.NullString{String: "Momentum detected", Valid: true},
		Confidence:   dbsql.NullString{String: "70", Valid: true},
	})
	if err != nil {
		log.Printf("Failed to record buy signal: %v", err)
	}

	position := &Position{
		TokenID:      tokenID,
		Market:       market,
		Shares:       shares,
		EntryPrice:   price,
		CurrentPrice: price,
		EntryTime:    time.Now(),
		UpdateChan:   updates,
	}

	s.positions[tokenID] = position
	s.currentBalance -= s.maxPositionSize

	marketName := market
	if len(marketName) > 30 {
		marketName = marketName[:27] + "..."
	}

	log.Printf("BOUGHT %.2f shares of %s at %.2f%% ($%.2f)",
		shares, marketName, price*100, s.maxPositionSize)
}

func (s *Strategy) monitorPositions() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkExitConditions()
		}
	}
}

func (s *Strategy) checkExitConditions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for tokenID, pos := range s.positions {
		book, err := s.pmClient.GetOrderBook(tokenID)
		if err != nil {
			continue
		}

		var bestBid float64
		if bids, ok := book["bids"].([]interface{}); ok && len(bids) > 0 {
			if bid, ok := bids[0].(map[string]interface{}); ok {
				if priceStr, ok := bid["price"].(string); ok {
					bestBid, _ = strconv.ParseFloat(priceStr, 64)
				}
			}
		}

		if bestBid == 0 {
			continue
		}

		pos.CurrentPrice = bestBid
		pnlPercent := (bestBid - pos.EntryPrice) / pos.EntryPrice

		shouldExit := false
		reason := ""

		if pnlPercent <= -s.stopLossPercent {
			shouldExit = true
			reason = fmt.Sprintf("Stop loss (%.1f%%)", pnlPercent*100)
		} else if pnlPercent >= s.takeProfitPercent {
			shouldExit = true
			reason = fmt.Sprintf("Take profit (%.1f%%)", pnlPercent*100)
		} else if time.Since(pos.EntryTime) > 4*time.Hour {
			shouldExit = true
			reason = "Time limit"
		}

		if shouldExit {
			s.exitPosition(tokenID, bestBid, reason)
		}
	}
}

func (s *Strategy) exitPosition(tokenID string, price float64, reason string) {
	pos, exists := s.positions[tokenID]
	if !exists {
		return
	}

	proceeds := pos.Shares * price
	pnl := proceeds - s.maxPositionSize

	_, err := s.store.RecordSignal(s.ctx, database.RecordSignalParams{
		SessionID:    dbsql.NullInt32{Int32: s.sessionID, Valid: true},
		TokenID:      tokenID,
		SignalType:   "sell",
		BestBid:      dbsql.NullString{String: fmt.Sprintf("%.6f", price), Valid: true},
		BestAsk:      dbsql.NullString{String: fmt.Sprintf("%.6f", price), Valid: true},
		ActionReason: dbsql.NullString{String: reason, Valid: true},
		Confidence:   dbsql.NullString{String: "90", Valid: true},
	})
	if err != nil {
		log.Printf("Failed to record sell signal: %v", err)
	}

	delete(s.positions, tokenID)
	s.currentBalance += proceeds

	marketName := pos.Market
	if len(marketName) > 30 {
		marketName = marketName[:27] + "..."
	}

	log.Printf("SOLD %.2f shares of %s at %.2f%% - %s (P&L: $%.2f)",
		pos.Shares, marketName, price*100, reason, pnl)
}

func (s *Strategy) statusUpdates() {
	// First update after 1 minute, then every 30 minutes
	time.Sleep(1 * time.Minute)
	s.printStatus()

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.printStatus()
		}
	}
}

func (s *Strategy) printStatus() {
	s.mu.Lock()
	defer s.mu.Unlock()

	totalValue := s.currentBalance
	for _, pos := range s.positions {
		totalValue += pos.Shares * pos.CurrentPrice
	}

	pnl := totalValue - s.initialBalance
	pnlPercent := pnl / s.initialBalance * 100

	log.Printf("STATUS: Balance: $%.2f, Positions: %d, Total Value: $%.2f (%.1f%%)",
		s.currentBalance, len(s.positions), totalValue, pnlPercent)

	err := s.store.UpdateSessionBalance(s.ctx, database.UpdateSessionBalanceParams{
		ID:             s.sessionID,
		CurrentBalance: dbsql.NullString{String: fmt.Sprintf("%.2f", totalValue), Valid: true},
	})
	if err != nil {
		log.Printf("Failed to update session balance: %v", err)
	}
}

func (s *Strategy) shutdown() {
	log.Println("Strategy shutting down...")

	s.mu.Lock()
	for tokenID, pos := range s.positions {
		price := pos.CurrentPrice
		if price == 0 {
			// Try to get current price
			if book, err := s.pmClient.GetOrderBook(tokenID); err == nil {
				if bids, ok := book["bids"].([]interface{}); ok && len(bids) > 0 {
					if bid, ok := bids[0].(map[string]interface{}); ok {
						if priceStr, ok := bid["price"].(string); ok {
							price, _ = strconv.ParseFloat(priceStr, 64)
						}
					}
				}
			}
		}
		s.exitPosition(tokenID, price, "Strategy ended")
	}
	s.mu.Unlock()

	err := s.store.EndSession(s.ctx, s.sessionID)
	if err != nil {
		log.Printf("Failed to end session: %v", err)
	}

	finalPnL := s.currentBalance - s.initialBalance
	log.Printf("FINAL: Started with $%.2f, Ended with $%.2f, P&L: $%.2f (%.1f%%)",
		s.initialBalance, s.currentBalance, finalPnL, finalPnL/s.initialBalance*100)
}

type MarketInfo struct {
	Question  string
	Active    bool
	Volume24h float64
	Tokens    []TokenInfo
}

type TokenInfo struct {
	TokenID string
	Outcome string
	Price   float64
}
