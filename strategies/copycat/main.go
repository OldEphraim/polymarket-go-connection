package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
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

type CopycatStrategy struct {
	*strategy.BaseStrategy

	// Configuration
	config *config.CopycatConfig

	// Managers and utilities
	posManager    *position.Manager
	wsManager     *websocket.Manager
	taskScheduler *scheduler.Scheduler
	logger        *logging.StrategyLogger
	perfTracker   *logging.PerformanceTracker

	// API clients
	dataAPI       *clients.PolymarketDataAPI
	marketFetcher market.Fetcher

	// Tracking
	traderActivity map[string][]clients.UserActivity
	lastCheckTime  map[string]time.Time
	startTime      time.Time // When strategy started
	checkCount     int
	tradesAnalyzed int
	tradesCopied   int
	tradesSkipped  int
}

func main() {
	// Parse command line flags
	configFile := flag.String("config", "copycat.json", "Configuration file")
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
	cfg, err := loader.LoadCopycatConfig(*configFile)
	if err != nil {
		log.Fatal("Failed to load config: ", err)
	}

	// Override from environment
	loader.LoadFromEnv(&cfg.BaseConfig)

	// Get duration from config
	duration, err := cfg.GetDuration()
	if err != nil {
		log.Fatal("Invalid duration in config: ", err)
	}

	log.Printf("=== STARTING COPYCAT STRATEGY ===")
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

	strategy, err := NewCopycatStrategy(ctx, cancel, cfg, level)
	if err != nil {
		log.Fatal(err)
	}

	strategy.Run()
}

func NewCopycatStrategy(ctx context.Context, cancel context.CancelFunc, cfg *config.CopycatConfig, logLevel logging.LogLevel) (*CopycatStrategy, error) {
	// Create logger
	logger, err := logging.NewStrategyLoggerWithLevel(cfg.Name, logLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	logger.Info("=== INITIALIZING COPYCAT STRATEGY ===")

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
			"scale_factor": %f,
			"max_position_size": %f,
			"max_positions": %d,
			"stop_loss": %f,
			"take_profit": %f,
			"smart_traders": %d
		}`, cfg.ScaleFactor, cfg.MaxPositionSize, cfg.MaxPositions,
			cfg.StopLoss, cfg.TakeProfit, len(cfg.SmartTraders))),
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
	marketFetcher := market.NewStandardFetcher(pmClient, wsClient)

	// Create performance tracker
	perfTracker := logging.NewPerformanceTracker(logger)

	s := &CopycatStrategy{
		BaseStrategy:   base,
		config:         cfg,
		posManager:     posManager,
		wsManager:      wsManager,
		logger:         logger,
		perfTracker:    perfTracker,
		dataAPI:        clients.NewPolymarketDataAPI(),
		marketFetcher:  marketFetcher,
		traderActivity: make(map[string][]clients.UserActivity),
		lastCheckTime:  make(map[string]time.Time),
		startTime:      time.Now(),
	}

	// Initialize last check time to 10 minutes before start
	// This allows us to catch very recent trades but not old ones
	for _, trader := range cfg.SmartTraders {
		s.lastCheckTime[trader.Address] = time.Now().Add(-10 * time.Minute)
	}

	// Build task scheduler
	s.taskScheduler = scheduler.NewBuilder().
		AddTask("check_traders", 30*time.Second, s.checkAllTraders).
		AddTask("check_exits", 20*time.Second, s.checkExitConditions).
		AddTask("status_update", 2*time.Minute, s.printStatus).
		AddTask("performance_log", 5*time.Minute, s.logPerformance).
		Build()

	logger.Info("=== CONFIGURATION ===")
	logger.Info("  Max Positions: %d", cfg.MaxPositions)
	logger.Info("  Max Position Size: $%.2f", cfg.MaxPositionSize)
	logger.Info("  Stop Loss: %.1f%%", cfg.StopLoss*100)
	logger.Info("  Take Profit: %.1f%%", cfg.TakeProfit*100)
	logger.Info("  Scale Factor: %.2f%%", cfg.ScaleFactor*100)
	logger.Info("  Following %d Smart Traders", len(cfg.SmartTraders))

	for _, trader := range cfg.SmartTraders {
		logger.Info("    • %s (%s): Score %.0f, Win Rate %.1f%%",
			trader.Nickname, trader.Address[:8], trader.SmartScore, trader.WinRate)
	}

	return s, nil
}

func (s *CopycatStrategy) Run() {
	s.logger.Info("=== STARTING STRATEGY COMPONENTS ===")

	// Start WebSocket manager
	go s.wsManager.MaintainConnection(s.Ctx)
	go s.WSClient.Listen(s.Ctx)

	// Wait a moment for connections to establish
	time.Sleep(2 * time.Second)

	// Initial check
	s.checkAllTraders()

	// Start scheduler (blocks until context done)
	s.taskScheduler.Run(s.Ctx)

	// Cleanup on exit
	s.shutdown()
}

func (s *CopycatStrategy) checkAllTraders() {
	s.checkCount++
	s.logger.Debug("Checking trader activity #%d", s.checkCount)

	for _, trader := range s.config.SmartTraders {
		s.checkTraderActivity(trader)
	}
}

func (s *CopycatStrategy) checkTraderActivity(trader config.TraderConfig) {
	activities, err := s.dataAPI.GetUserActivity(trader.Address, 20)
	if err != nil {
		s.logger.Warn("Failed to check %s: %v", trader.Nickname, err)
		return
	}

	s.Mu.Lock()
	lastCheck := s.lastCheckTime[trader.Address]
	s.lastCheckTime[trader.Address] = time.Now()
	s.traderActivity[trader.Address] = activities
	s.Mu.Unlock()

	for _, activity := range activities {
		activityTime := time.Unix(activity.Timestamp, 0)

		// Skip trades from before we last checked
		if activityTime.Before(lastCheck) {
			continue
		}

		// Skip non-trade activities
		if activity.Type != "TRADE" {
			continue
		}

		s.tradesAnalyzed++

		if s.shouldCopyTrade(trader, activity) {
			s.copyTrade(trader, activity)
		} else {
			s.tradesSkipped++
		}
	}
}

func (s *CopycatStrategy) shouldCopyTrade(trader config.TraderConfig, activity clients.UserActivity) bool {
	// Only copy BUY orders
	if activity.Side != "BUY" {
		s.logger.Debug("Skipping SELL trade from %s", trader.Nickname)
		return false
	}

	// Skip extreme prices
	if activity.Price < 0.05 || activity.Price > 0.95 {
		s.logger.Debug("Skipping extreme price %.3f from %s", activity.Price, trader.Nickname)
		return false
	}

	// Maximum age for trades - 2 minutes for very fresh trades only
	tradeAge := time.Since(time.Unix(activity.Timestamp, 0))
	if tradeAge > 2*time.Minute {
		s.logger.Debug("Trade too old: %.1f minutes from %s", tradeAge.Minutes(), trader.Nickname)
		return false
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()

	// Check if we already have this position
	positionKey := fmt.Sprintf("%s-%s", trader.Address, activity.Asset)
	if _, exists := s.Positions[positionKey]; exists {
		s.logger.Debug("Already have position for %s from %s", activity.Asset[:8], trader.Nickname)
		return false
	}

	// Use position manager to check if we can enter
	desiredSize := activity.USDCSize * s.config.ScaleFactor
	ourSize := s.posManager.CalculatePositionSize(desiredSize, 5)

	if !s.posManager.CanEnter(s.CurrentBalance, len(s.Positions), ourSize) {
		s.logger.Debug("Cannot enter: insufficient balance or max positions reached")
		return false
	}

	return true
}

func (s *CopycatStrategy) copyTrade(trader config.TraderConfig, activity clients.UserActivity) {
	// Get CURRENT market prices from orderbook
	book, err := s.marketFetcher.GetOrderBook(activity.Asset)
	if err != nil {
		s.logger.Warn("Failed to get orderbook for %s: %v", activity.Asset[:20], err)
		return
	}

	currentAsk := common.ExtractBestAsk(book)
	currentBid := common.ExtractBestBid(book)

	// Validate orderbook data
	if currentAsk <= 0 || currentBid <= 0 {
		s.logger.Warn("Invalid orderbook prices for %s: bid=%.4f, ask=%.4f",
			activity.Asset[:20], currentBid, currentAsk)
		return
	}

	// Check spread
	spread := common.CalculateSpread(currentBid, currentAsk)
	if spread > 0.05 { // 5% max spread
		s.logger.Warn("Spread too wide for %s: %.2f%%", activity.Asset[:20], spread*100)
		return
	}

	// Check price slippage from trader's entry
	priceSlippage := (currentAsk - activity.Price) / activity.Price
	if priceSlippage > 0.10 { // More than 10% higher than trader paid
		s.logger.Warn("Price slippage too high for %s: trader paid %.4f, now %.4f (%.1f%%)",
			activity.Asset[:20], activity.Price, currentAsk, priceSlippage*100)
		return
	}

	if priceSlippage < -0.20 { // Price dropped more than 20% - might be collapsing
		s.logger.Warn("Price dropped significantly for %s: trader paid %.4f, now %.4f (%.1f%%)",
			activity.Asset[:20], activity.Price, currentAsk, priceSlippage*100)
		return
	}

	// Calculate position size based on CURRENT price
	desiredSize := activity.USDCSize * s.config.ScaleFactor
	ourSize := s.posManager.CalculatePositionSize(desiredSize, 5)
	shares := ourSize / currentAsk // Use current ask price

	reason := fmt.Sprintf("Copying %s (score: %.0f)", trader.Nickname, trader.SmartScore)

	// Log entry with both prices
	s.logger.LogEntry(activity.Asset, activity.Title, currentAsk, ourSize, reason)
	s.logger.Info("  Master: %s (%s)", trader.Nickname, trader.Address[:8])
	s.logger.Info("  Master paid: $%.4f → Current ask: $%.4f (slippage: %.1f%%)",
		activity.Price, currentAsk, priceSlippage*100)
	s.logger.Info("  Their Size: $%.2f → Our Size: $%.2f", activity.USDCSize, ourSize)
	s.logger.Info("  Spread: %.2f%%", spread*100)

	// Record signal with CURRENT prices
	signalID, err := dbops.RecordSignal(s.Ctx, s.Store, dbops.SignalParams{
		SessionID:    s.SessionID,
		TokenID:      activity.Asset,
		SignalType:   "buy",
		BestBid:      currentBid, // Current bid
		BestAsk:      currentAsk, // Current ask
		ActionReason: reason,
		Confidence:   trader.SmartScore,
	})

	if err != nil {
		s.logger.Error("Failed to record signal: %v", err)
	} else {
		s.logger.Debug("Signal %d recorded", signalID)
	}

	// Subscribe to price updates using WebSocket manager
	s.wsManager.MonitorPriceUpdates(activity.Asset, func(update client.MarketUpdate) {
		s.updatePositionPrice(fmt.Sprintf("%s-%s", trader.Address, activity.Asset), update)
	})

	// Store position with CURRENT market price as entry
	s.Mu.Lock()
	positionKey := fmt.Sprintf("%s-%s", trader.Address, activity.Asset)
	s.Positions[positionKey] = &strategy.Position{
		TokenID:      activity.Asset,
		Market:       activity.Title,
		Shares:       shares,
		EntryPrice:   currentAsk, // Our actual entry price (current ask)
		CurrentPrice: currentAsk,
		EntryTime:    time.Now(),
		Metadata: map[string]interface{}{
			"masterAddress":  trader.Address,
			"masterNickname": trader.Nickname,
			"masterPrice":    activity.Price, // What master paid
			"masterSize":     activity.USDCSize,
			"ourSize":        ourSize,
			"slippage":       priceSlippage, // Track price difference
			"spread":         spread,
		},
	}
	s.CurrentBalance -= ourSize
	s.tradesCopied++
	s.Mu.Unlock()
}

func (s *CopycatStrategy) updatePositionPrice(posKey string, update client.MarketUpdate) {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	if pos, exists := s.Positions[posKey]; exists && update.BestBid > 0 {
		// Sanity check: ignore extreme price movements (likely bad data)
		priceChange := (update.BestBid - pos.CurrentPrice) / pos.CurrentPrice
		if math.Abs(priceChange) > 0.50 { // 50% in one update is suspicious
			s.logger.Warn("Ignoring suspicious price update for %s: %.4f → %.4f (%.1f%% change)",
				posKey[:16], pos.CurrentPrice, update.BestBid, priceChange*100)
			return
		}

		pos.CurrentPrice = update.BestBid
		s.logger.LogMetric(fmt.Sprintf("price_%s", posKey[:16]), update.BestBid)
	}
}

func (s *CopycatStrategy) checkExitConditions() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	for key, pos := range s.Positions {
		// Update price from orderbook
		book, err := s.marketFetcher.GetOrderBook(pos.TokenID)
		if err == nil {
			if bestBid := common.ExtractBestBid(book); bestBid > 0 {
				// Sanity check here too
				priceChange := (bestBid - pos.CurrentPrice) / pos.CurrentPrice
				if math.Abs(priceChange) <= 0.50 { // Only update if reasonable
					pos.CurrentPrice = bestBid
				}
			}
		}

		// Check standard exit conditions using position manager
		params := position.ExitParams{
			CurrentPrice: pos.CurrentPrice,
			EntryPrice:   pos.EntryPrice,
			EntryTime:    pos.EntryTime,
			Metadata:     pos.Metadata,
		}

		// Custom check for master exit
		shouldExit, reason := s.posManager.CheckExitWithCustomRules(params, func(p position.ExitParams) (bool, string) {
			metadata := p.Metadata
			masterAddress := metadata["masterAddress"].(string)
			masterNickname := metadata["masterNickname"].(string)

			if !s.checkMasterStillIn(masterAddress, pos.TokenID) {
				return true, fmt.Sprintf("Master exited (%s)", masterNickname)
			}
			return false, ""
		})

		if shouldExit {
			s.exitPosition(key, pos.CurrentPrice, reason)
		}
	}
}

func (s *CopycatStrategy) checkMasterStillIn(masterAddress, assetID string) bool {
	activities, err := s.dataAPI.GetUserActivity(masterAddress, 10)
	if err != nil {
		s.logger.Warn("Failed to check master position: %v", err)
		return true // Assume still in if we can't check
	}

	for _, activity := range activities {
		if activity.Asset == assetID && activity.Side == "SELL" {
			if time.Since(time.Unix(activity.Timestamp, 0)) < 1*time.Hour {
				return false // Master has exited recently
			}
		}
	}
	return true
}

func (s *CopycatStrategy) exitPosition(key string, price float64, reason string) {
	pos := s.Positions[key]
	if pos == nil {
		return
	}

	metadata := pos.Metadata
	ourSize := metadata["ourSize"].(float64)
	proceeds := pos.Shares * price
	pnl, _ := s.posManager.CalculatePnLFromCost(ourSize, proceeds)

	s.logger.LogExit(pos.TokenID, pos.EntryPrice, price, pnl, reason)
	s.perfTracker.RecordTrade(pnl)

	// Log additional context
	masterPrice := metadata["masterPrice"].(float64)
	s.logger.Debug("Master entry: %.4f, Our entry: %.4f, Exit: %.4f",
		masterPrice, pos.EntryPrice, price)

	dbops.RecordSignal(s.Ctx, s.Store, dbops.SignalParams{
		SessionID:    s.SessionID,
		TokenID:      pos.TokenID,
		SignalType:   "sell",
		BestBid:      price,
		BestAsk:      price,
		ActionReason: reason,
		Confidence:   100,
	})

	delete(s.Positions, key)
	s.CurrentBalance += proceeds
}

func (s *CopycatStrategy) printStatus() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	totalValue := s.CurrentBalance
	for _, pos := range s.Positions {
		totalValue += pos.Shares * pos.CurrentPrice
	}

	pnl := totalValue - s.config.InitialBalance
	s.perfTracker.UpdateBalance(totalValue)

	s.logger.LogStatus(totalValue, len(s.Positions), pnl)
	s.logger.Info("  Trades Analyzed: %d", s.tradesAnalyzed)
	s.logger.Info("  Trades Copied: %d", s.tradesCopied)
	s.logger.Info("  Trades Skipped: %d", s.tradesSkipped)
	s.logger.Info("  Runtime: %v", time.Since(s.startTime).Round(time.Second))

	dbops.UpdateSessionBalance(s.Ctx, s.Store, s.SessionID, totalValue)
}

func (s *CopycatStrategy) logPerformance() {
	s.perfTracker.LogSummary()
}

func (s *CopycatStrategy) shutdown() {
	s.logger.Info("=== SHUTTING DOWN ===")

	s.Mu.Lock()
	for key, pos := range s.Positions {
		price := pos.CurrentPrice
		if price == 0 {
			price = pos.EntryPrice
		}
		s.exitPosition(key, price, "Strategy shutdown")
	}
	s.Mu.Unlock()

	dbops.EndSession(s.Ctx, s.Store, s.SessionID)

	// Log final performance
	s.perfTracker.LogSummary()
	s.logger.Info("Total trades analyzed: %d", s.tradesAnalyzed)
	s.logger.Info("Total trades copied: %d", s.tradesCopied)
	s.logger.Info("Total trades skipped: %d", s.tradesSkipped)
	s.logger.Info("Total runtime: %v", time.Since(s.startTime))

	// Close logger
	s.logger.Close()
}
