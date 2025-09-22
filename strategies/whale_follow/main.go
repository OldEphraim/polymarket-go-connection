package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/utils/clients"
	"github.com/OldEphraim/polymarket-go-connection/utils/common"
	"github.com/OldEphraim/polymarket-go-connection/utils/config"
	"github.com/OldEphraim/polymarket-go-connection/utils/dbops"
	"github.com/OldEphraim/polymarket-go-connection/utils/logging"
	"github.com/OldEphraim/polymarket-go-connection/utils/market"
	"github.com/OldEphraim/polymarket-go-connection/utils/position"
	"github.com/OldEphraim/polymarket-go-connection/utils/scheduler"
	"github.com/OldEphraim/polymarket-go-connection/utils/strategy"
	"github.com/OldEphraim/polymarket-go-connection/utils/websocket"
	"github.com/joho/godotenv"
)

type WhaleFollowStrategy struct {
	*strategy.BaseStrategy

	// Configuration
	config *config.WhaleConfig

	// Managers and utilities
	posManager    *position.Manager
	wsManager     *websocket.Manager
	taskScheduler *scheduler.Scheduler
	logger        *logging.StrategyLogger
	perfTracker   *logging.PerformanceTracker

	// API clients
	hashdiveAPI   *clients.HashdiveAPI
	marketFetcher market.Fetcher

	// Tracking
	whaleCache map[string]time.Time
	checkCount int
	tradesSeen int
}

func main() {
	// Parse command line flags
	configFile := flag.String("config", "whale.json", "Configuration file")
	generateConfig := flag.Bool("generate-config", false, "Generate default configuration")
	logLevel := flag.String("log-level", "INFO", "Log level (DEBUG, INFO, WARN, ERROR)")
	flag.Parse()

	// Load environment
	godotenv.Load()
	rand.Seed(time.Now().UnixNano())

	// Generate config if requested
	if *generateConfig {
		loader := config.NewLoader()
		if err := loader.GenerateDefaultConfigs(); err != nil {
			log.Fatal("Failed to generate configs: ", err)
		}
		log.Println("Configuration files generated in configs/")
		return
	}

	// Load configuration
	loader := config.NewLoader()
	cfg, err := loader.LoadWhaleConfig(*configFile)
	if err != nil {
		log.Fatal("Failed to load config: ", err)
	}

	// Override from environment
	loader.LoadFromEnv(&cfg.BaseConfig)

	// Check API key
	apiKey := os.Getenv("HASHDIVE_API_KEY")
	if apiKey == "" {
		log.Fatal("HASHDIVE_API_KEY not set")
	}

	// Get duration from config
	duration, err := cfg.GetDuration()
	if err != nil {
		log.Fatal("Invalid duration in config: ", err)
	}

	log.Printf("=== STARTING WHALE FOLLOW STRATEGY ===")
	log.Printf("Configuration: %s", *configFile)
	if cfg.IsInfinite() {
		log.Printf("Duration: INFINITE (manual stop required)")
	} else {
		log.Printf("Duration: %v", duration)
	}
	log.Printf("Initial Balance: $%.2f", cfg.InitialBalance)

	// Create context based on duration
	var ctx context.Context
	var cancel context.CancelFunc
	if cfg.IsInfinite() {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), duration)
	}
	defer cancel()

	// Parse log level
	level := logging.INFO
	switch *logLevel {
	case "DEBUG":
		level = logging.DEBUG
	case "WARN":
		level = logging.WARN
	case "ERROR":
		level = logging.ERROR
	}

	strategy, err := NewWhaleFollowStrategy(ctx, cancel, cfg, level, apiKey)
	if err != nil {
		log.Fatal(err)
	}

	strategy.Run()
}

func NewWhaleFollowStrategy(ctx context.Context, cancel context.CancelFunc, cfg *config.WhaleConfig, logLevel logging.LogLevel, apiKey string) (*WhaleFollowStrategy, error) {
	// Create logger
	logger, err := logging.NewStrategyLoggerWithLevel(cfg.Name, logLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	logger.Info("=== INITIALIZING WHALE FOLLOW STRATEGY ===")

	// Initialize base strategy
	base := &strategy.BaseStrategy{
		InitialBalance: cfg.InitialBalance,
		CurrentBalance: cfg.InitialBalance,
		Positions:      make(map[string]*strategy.Position),
		Ctx:            ctx,
		Cancel:         cancel,
	}

	// Initialize database
	store, err := db.NewStore(dbops.GetDBConnection())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	base.Store = store
	logger.Info("✓ Database connected")

	// Initialize strategy and session
	dbConfig := dbops.StrategyConfig{
		Name: cfg.Name,
		Config: json.RawMessage(fmt.Sprintf(`{
			"min_whale_size": %f,
			"follow_percent": %f,
			"max_positions": %d,
			"max_position_size": %f,
			"stop_loss": %f,
			"take_profit": %f,
			"whale_exit_trigger": %f
		}`, cfg.MinWhaleSize, cfg.FollowPercent, cfg.MaxPositions,
			cfg.MaxPositionSize, cfg.StopLoss, cfg.TakeProfit,
			cfg.WhaleExitTrigger)),
		InitialBalance: cfg.InitialBalance,
	}

	strategyID, sessionID, err := dbops.InitializeStrategy(ctx, store, dbConfig)
	if err != nil {
		return nil, err
	}
	base.SessionID = sessionID
	logger.Info("✓ Strategy %d, Session %d created", strategyID, sessionID)

	// Initialize clients
	pmClient, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		return nil, fmt.Errorf("failed to create Polymarket client: %w", err)
	}
	base.PMClient = pmClient

	wsClient, err := client.NewWSClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create WebSocket client: %w", err)
	}
	base.WSClient = wsClient

	// Create managers
	posManager := position.NewManager(
		cfg.MaxPositions,
		cfg.MaxPositionSize,
		cfg.StopLoss,
		cfg.TakeProfit,
		0, // No max hold time - using strategy duration instead
	)

	wsManager := websocket.NewManager(wsClient)
	hashdiveAPI := clients.NewHashdiveAPI(apiKey)
	marketFetcher := market.NewStandardFetcher(pmClient, wsClient)

	// Create performance tracker
	perfTracker := logging.NewPerformanceTracker(logger)

	s := &WhaleFollowStrategy{
		BaseStrategy:  base,
		config:        cfg,
		posManager:    posManager,
		wsManager:     wsManager,
		logger:        logger,
		perfTracker:   perfTracker,
		hashdiveAPI:   hashdiveAPI,
		marketFetcher: marketFetcher,
		whaleCache:    make(map[string]time.Time),
	}

	// Build task scheduler
	s.taskScheduler = scheduler.NewBuilder().
		AddTask("check_whales", 1*time.Minute, s.checkWhales).
		AddTask("check_exits", 30*time.Second, s.checkExitConditions).
		AddTask("status_update", 2*time.Minute, s.printStatus).
		AddTask("performance_log", 5*time.Minute, s.logPerformance).
		AddTask("cache_cleanup", 1*time.Hour, s.cleanWhaleCache).
		Build()

	logger.Info("=== CONFIGURATION ===")
	logger.Info("  Max Positions: %d", cfg.MaxPositions)
	logger.Info("  Max Position Size: $%.2f", cfg.MaxPositionSize)
	logger.Info("  Stop Loss: %.1f%%", cfg.StopLoss*100)
	logger.Info("  Take Profit: %.1f%%", cfg.TakeProfit*100)
	logger.Info("  Min Whale Size: $%.0f", cfg.MinWhaleSize)
	logger.Info("  Follow Percent: %.2f%%", cfg.FollowPercent*100)
	logger.Info("  Whale Exit Trigger: %.0f%%", cfg.WhaleExitTrigger*100)

	return s, nil
}

func (s *WhaleFollowStrategy) Run() {
	s.logger.Info("=== STARTING STRATEGY COMPONENTS ===")

	// Start WebSocket manager
	go s.wsManager.MaintainConnection(s.Ctx)
	go s.WSClient.Listen(s.Ctx)

	// Initial whale check
	s.checkWhales()

	// Start scheduler (blocks until context done)
	s.taskScheduler.Run(s.Ctx)

	// Cleanup
	s.shutdown()
}

func (s *WhaleFollowStrategy) checkWhales() {
	s.checkCount++
	s.logger.Debug("Whale check #%d", s.checkCount)

	// Use Hashdive API client
	trades, err := s.hashdiveAPI.GetWhaleTrades(s.config.MinWhaleSize, 20)
	if err != nil {
		s.logger.Error("Failed to fetch whale trades: %v", err)
		return
	}

	s.logger.Debug("Received %d whale trades", len(trades))

	newTrades := 0
	for _, trade := range trades {
		tradeTime := time.Unix(trade.Timestamp, 0)
		cacheKey := fmt.Sprintf("%s-%s-%d", trade.UserAddress, trade.AssetID, trade.Timestamp)

		s.Mu.Lock()
		if _, exists := s.whaleCache[cacheKey]; exists {
			s.Mu.Unlock()
			continue
		}
		s.whaleCache[cacheKey] = time.Now()
		s.Mu.Unlock()

		s.tradesSeen++
		newTrades++

		if s.shouldFollow(trade, tradeTime) {
			s.enterWhalePosition(trade)
		}
	}

	if newTrades > 0 {
		s.logger.Info("Analyzed %d new whale trades", newTrades)
	}
}

func (s *WhaleFollowStrategy) shouldFollow(trade clients.WhaleTrade, tradeTime time.Time) bool {
	// Basic filters
	if trade.Side != "buy" || trade.Price < 0.05 || trade.Price > 0.95 {
		s.logger.Debug("Skipping whale trade: side=%s, price=%.3f", trade.Side, trade.Price)
		return false
	}

	if time.Since(tradeTime) > 5*time.Minute {
		s.logger.Debug("Skipping old whale trade from %v ago", time.Since(tradeTime))
		return false
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()

	// Check if we already have this position
	if _, exists := s.Positions[trade.AssetID]; exists {
		return false
	}

	// Use position manager to check if we can enter
	requiredSize := trade.USDSize * s.config.FollowPercent
	if requiredSize < 10 {
		requiredSize = 10
	}

	return s.posManager.CanEnter(s.CurrentBalance, len(s.Positions), requiredSize)
}

func (s *WhaleFollowStrategy) enterWhalePosition(whale clients.WhaleTrade) {
	desiredSize := whale.USDSize * s.config.FollowPercent
	ourSize := s.posManager.CalculatePositionSize(desiredSize, 10)
	shares := ourSize / whale.Price

	// Calculate confidence based on whale size
	confidence := 50.0 + (whale.USDSize/100000.0)*50.0
	if confidence > 100 {
		confidence = 100
	}

	reason := fmt.Sprintf("Following whale %s ($%.0f trade)", whale.UserAddress[:8], whale.USDSize)

	s.logger.LogEntry(whale.AssetID, whale.MarketInfo.Question, whale.Price, ourSize, reason)
	s.logger.Info("  Whale: %s", whale.UserAddress[:8])
	s.logger.Info("  Whale Size: $%.0f → Our Size: $%.2f", whale.USDSize, ourSize)
	s.logger.LogSignal("BUY", whale.AssetID, confidence, reason)

	// Subscribe to price updates
	s.wsManager.MonitorPriceUpdates(whale.AssetID, func(update client.MarketUpdate) {
		s.updatePositionPrice(whale.AssetID, update)
	})

	// Record signal
	signalID, err := dbops.RecordSignal(s.Ctx, s.Store, dbops.SignalParams{
		SessionID:    s.SessionID,
		TokenID:      whale.AssetID,
		SignalType:   "buy",
		BestBid:      whale.Price,
		BestAsk:      whale.Price,
		ActionReason: reason,
		Confidence:   confidence,
	})

	if err != nil {
		s.logger.Error("Failed to record signal: %v", err)
	} else {
		s.logger.Debug("Signal %d recorded", signalID)
	}

	// Store position
	s.Mu.Lock()
	s.Positions[whale.AssetID] = &strategy.Position{
		TokenID:      whale.AssetID,
		Market:       whale.MarketInfo.Question,
		Shares:       shares,
		EntryPrice:   whale.Price,
		CurrentPrice: whale.Price,
		EntryTime:    time.Now(),
		Metadata: map[string]interface{}{
			"whaleAddress":       whale.UserAddress,
			"whaleSize":          whale.USDSize,
			"whaleInitialAmount": whale.Size,
			"ourSize":            ourSize,
		},
	}
	s.CurrentBalance -= ourSize
	s.Mu.Unlock()
}

func (s *WhaleFollowStrategy) updatePositionPrice(tokenID string, update client.MarketUpdate) {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	if pos, exists := s.Positions[tokenID]; exists && update.BestBid > 0 {
		pos.CurrentPrice = update.BestBid
		s.logger.LogMetric(fmt.Sprintf("price_%s", tokenID[:16]), update.BestBid)
	}
}

func (s *WhaleFollowStrategy) checkExitConditions() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	for tokenID, pos := range s.Positions {
		// Update price from orderbook
		book, err := s.marketFetcher.GetOrderBook(tokenID)
		if err == nil {
			if bestBid := common.ExtractBestBid(book); bestBid > 0 {
				pos.CurrentPrice = bestBid
			}
		}

		// Standard exit checks
		params := position.ExitParams{
			CurrentPrice: pos.CurrentPrice,
			EntryPrice:   pos.EntryPrice,
			EntryTime:    pos.EntryTime,
			Metadata:     pos.Metadata,
		}

		// Custom whale-specific exit rules
		shouldExit, reason := s.posManager.CheckExitWithCustomRules(params, func(p position.ExitParams) (bool, string) {
			return s.checkWhaleExit(p, tokenID)
		})

		// Add time-based exit for data collection
		holdTime := time.Since(pos.EntryTime)
		if holdTime > 4*time.Hour {
			shouldExit = true
			reason = "Max hold time (4h) for data collection"
		}

		if shouldExit {
			s.exitPosition(tokenID, pos.CurrentPrice, reason)
		}
	}
}

func (s *WhaleFollowStrategy) checkWhaleExit(params position.ExitParams, tokenID string) (bool, string) {
	metadata := params.Metadata
	whaleAddress := metadata["whaleAddress"].(string)
	whaleInitialSize := metadata["whaleInitialAmount"].(float64)

	// Check if whale has exited
	whalePos, err := s.hashdiveAPI.GetWhalePosition(whaleAddress, tokenID)
	if err != nil {
		s.logger.Warn("Failed to check whale position: %v", err)
		return false, ""
	}

	if whalePos != nil && whalePos.Amount < whaleInitialSize*s.config.WhaleExitTrigger {
		reduction := (1 - whalePos.Amount/whaleInitialSize) * 100
		return true, fmt.Sprintf("Whale reduced position by %.0f%%", reduction)
	}

	return false, ""
}

func (s *WhaleFollowStrategy) exitPosition(tokenID string, price float64, reason string) {
	pos := s.Positions[tokenID]
	if pos == nil {
		return
	}

	metadata := pos.Metadata
	ourSize := metadata["ourSize"].(float64)
	proceeds := pos.Shares * price
	pnl, _ := s.posManager.CalculatePnLFromCost(ourSize, proceeds)

	s.logger.LogExit(tokenID, pos.EntryPrice, price, pnl, reason)
	s.perfTracker.RecordTrade(pnl)

	dbops.RecordSignal(s.Ctx, s.Store, dbops.SignalParams{
		SessionID:    s.SessionID,
		TokenID:      tokenID,
		SignalType:   "sell",
		BestBid:      price,
		BestAsk:      price,
		ActionReason: reason,
		Confidence:   100,
	})

	delete(s.Positions, tokenID)
	s.CurrentBalance += proceeds
}

func (s *WhaleFollowStrategy) printStatus() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	totalValue := s.CurrentBalance

	// Show position details
	for _, pos := range s.Positions {
		summary := s.posManager.GetPositionSummary(pos)
		totalValue += pos.Shares * pos.CurrentPrice

		metadata := pos.Metadata
		whaleAddr := metadata["whaleAddress"].(string)

		s.logger.Debug("Position %s (whale %s): %.1f%% in %.1fh",
			common.TruncateString(summary.Market, 25),
			whaleAddr[:6],
			summary.PnLPercent*100,
			summary.HoldTime.Hours())
	}

	pnl := totalValue - s.config.InitialBalance
	s.perfTracker.UpdateBalance(totalValue)

	s.logger.LogStatus(totalValue, len(s.Positions), pnl)
	s.logger.Info("  Whales Tracked: %d", s.tradesSeen)
	s.logger.Info("  Whale Checks: %d", s.checkCount)
	s.logger.Info("  Cache Size: %d entries", len(s.whaleCache))

	dbops.UpdateSessionBalance(s.Ctx, s.Store, s.SessionID, totalValue)
}

func (s *WhaleFollowStrategy) logPerformance() {
	s.perfTracker.LogSummary()
}

func (s *WhaleFollowStrategy) cleanWhaleCache() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	// Remove cache entries older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour)
	oldCount := len(s.whaleCache)

	for key, timestamp := range s.whaleCache {
		if timestamp.Before(cutoff) {
			delete(s.whaleCache, key)
		}
	}

	cleaned := oldCount - len(s.whaleCache)
	if cleaned > 0 {
		s.logger.Debug("Cleaned %d expired entries from whale cache, %d remaining",
			cleaned, len(s.whaleCache))
	}
}

func (s *WhaleFollowStrategy) shutdown() {
	s.logger.Info("=== SHUTTING DOWN ===")

	s.Mu.Lock()
	for tokenID, pos := range s.Positions {
		price := pos.CurrentPrice
		if price == 0 {
			price = pos.EntryPrice
		}
		s.exitPosition(tokenID, price, "Strategy shutdown")
	}
	s.Mu.Unlock()

	dbops.EndSession(s.Ctx, s.Store, s.SessionID)

	// Log final performance
	s.perfTracker.LogSummary()
	s.logger.Info("Total whales analyzed: %d", s.tradesSeen)
	s.logger.Info("Total checks performed: %d", s.checkCount)

	// Close logger
	s.logger.Close()
}
