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

type MomentumStrategy struct {
	*strategy.BaseStrategy

	// Configuration
	config *config.MomentumConfig

	// Managers and utilities
	posManager    *position.Manager
	wsManager     *websocket.Manager
	taskScheduler *scheduler.Scheduler
	marketFetcher market.Fetcher
	searchService *market.SearchService
	logger        *logging.StrategyLogger
	perfTracker   *logging.PerformanceTracker

	// Strategy state
	updateCounter int
	lastPrices    map[string]float64
}

func main() {
	// Parse command line flags
	configFile := flag.String("config", "momentum.json", "Configuration file")
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
	cfg, err := loader.LoadMomentumConfig(*configFile)
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

	log.Printf("=== STARTING MOMENTUM STRATEGY ===")
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

	strategy, err := NewMomentumStrategy(ctx, cancel, cfg, level)
	if err != nil {
		log.Fatal(err)
	}

	strategy.Run()
}

func NewMomentumStrategy(ctx context.Context, cancel context.CancelFunc, cfg *config.MomentumConfig, logLevel logging.LogLevel) (*MomentumStrategy, error) {
	// Create logger
	logger, err := logging.NewStrategyLoggerWithLevel(cfg.Name, logLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	logger.Info("=== INITIALIZING MOMENTUM STRATEGY ===")

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
            "max_position_size": %f,
            "stop_loss": %f,
            "take_profit": %f,
            "max_spread": %f,
            "min_volume_24h": %f,
            "momentum_threshold": %f
        }`, cfg.MaxPositionSize, cfg.StopLoss, cfg.TakeProfit,
			cfg.MaxSpreadPercent, cfg.MinVolume24h, cfg.MomentumThreshold)),
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

	// Create search service with filters
	searchService := market.NewSearchService()
	searchService.SetFilters(market.SearchFilters{
		MinVolume24hr: cfg.MinVolume24h,
		ActiveOnly:    true,
		MinPrice:      0.05,
		MaxPrice:      0.95,
		MaxResults:    20,
	})

	// Create performance tracker
	perfTracker := logging.NewPerformanceTracker(logger)

	s := &MomentumStrategy{
		BaseStrategy:  base,
		config:        cfg,
		posManager:    posManager,
		wsManager:     wsManager,
		marketFetcher: marketFetcher,
		searchService: searchService,
		logger:        logger,
		perfTracker:   perfTracker,
		lastPrices:    make(map[string]float64),
	}

	// Build task scheduler
	s.taskScheduler = scheduler.NewBuilder().
		AddTask("discover_markets", 30*time.Minute, s.discoverMarkets).
		AddTask("check_exits", 5*time.Second, s.checkExitConditions).
		AddTask("status_update", 30*time.Second, s.printStatus).
		AddTask("performance_log", 5*time.Minute, s.logPerformance).
		Build()

	logger.Info("=== CONFIGURATION ===")
	logger.Info("  Max Positions: %d", cfg.MaxPositions)
	logger.Info("  Max Position Size: $%.2f", cfg.MaxPositionSize)
	logger.Info("  Stop Loss: %.1f%%", cfg.StopLoss*100)
	logger.Info("  Take Profit: %.1f%%", cfg.TakeProfit*100)
	logger.Info("  Min Volume: $%.0f", cfg.MinVolume24h)
	logger.Info("  Max Spread: %.1f%%", cfg.MaxSpreadPercent*100)
	logger.Info("  Momentum Threshold: %.2f%%", cfg.MomentumThreshold*100)
	logger.Info("  Search Queries: %v", cfg.SearchQueries)

	return s, nil
}

func (s *MomentumStrategy) Run() {
	s.logger.Info("=== STARTING STRATEGY COMPONENTS ===")

	// Start WebSocket manager
	go s.wsManager.MaintainConnection(s.Ctx)
	go s.WSClient.Listen(s.Ctx)

	// Initial market discovery
	s.discoverMarkets()

	if s.config.TestMode {
		s.logger.Info("TEST MODE ENABLED")
		go s.simulateMarketActivity()
	}

	// Start scheduler (blocks until context done)
	s.taskScheduler.Run(s.Ctx)

	// Cleanup
	s.shutdown()
}

func (s *MomentumStrategy) simulateMarketActivity() {
	s.logger.Debug("Starting market simulation for testing")
	time.Sleep(15 * time.Second)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-ticker.C:
			s.Mu.Lock()
			if len(s.Positions) == 0 && s.posManager.CanEnter(s.CurrentBalance, 0, s.config.MaxPositionSize) {
				s.logger.Debug("TEST MODE: Simulating buy conditions")
			}
			s.Mu.Unlock()
		}
	}
}

func (s *MomentumStrategy) discoverMarkets() {
	s.logger.Info("=== DISCOVERING MARKETS ===")

	// Use search service to find markets
	results, err := s.searchService.FindMarkets(s.config.SearchQueries)
	if err != nil {
		s.logger.Error("Failed to search markets: %v", err)
		return
	}

	s.logger.Info("Found %d markets matching filters", len(results))

	subscribed := 0
	maxSubscriptions := 10

	for _, market := range results {
		if subscribed >= maxSubscriptions {
			break
		}

		for _, token := range market.Tokens {
			if subscribed >= maxSubscriptions {
				break
			}

			// Additional spread check before subscribing
			book, err := s.marketFetcher.GetOrderBook(token.TokenID)
			if err == nil {
				bid := common.ExtractBestBid(book)
				ask := common.ExtractBestAsk(book)
				if bid > 0 && ask > 0 {
					spread := common.CalculateSpread(bid, ask)
					if spread > s.config.MaxSpreadPercent {
						s.logger.Debug("Skipping %s - spread too wide (%.2f%%)",
							token.TokenID[:20], spread*100)
						continue
					}
				}
			}

			// Subscribe using WebSocket manager
			err = s.wsManager.Subscribe(token.TokenID, func(update client.MarketUpdate) {
				s.handleMarketUpdate(token, market.Question, update)
			})

			if err == nil {
				subscribed++
				s.lastPrices[token.TokenID] = token.Price
				s.logger.Info("✓ Subscribed to %s @ %.3f",
					common.TruncateString(market.Question, 40), token.Price)
			}
		}
	}

	s.logger.Info("Subscribed to %d markets", subscribed)
}

func (s *MomentumStrategy) handleMarketUpdate(token market.TokenResult, marketName string, update client.MarketUpdate) {
	if update.EventType != "book" {
		return
	}

	s.updateCounter++

	// Check spread
	if update.BestBid > 0 && update.BestAsk > 0 {
		spread := common.CalculateSpread(update.BestBid, update.BestAsk)
		if spread > s.config.MaxSpreadPercent {
			return
		}
	}

	// Momentum detection
	s.Mu.Lock()
	_, hasPosition := s.Positions[token.TokenID]
	canEnter := s.posManager.CanEnter(s.CurrentBalance, len(s.Positions), s.config.MaxPositionSize)
	lastPrice := s.lastPrices[token.TokenID]
	s.Mu.Unlock()

	if !hasPosition && canEnter && lastPrice > 0 {
		// Calculate momentum
		priceChange := (update.BestAsk - lastPrice) / lastPrice

		if s.config.TestMode || priceChange > s.config.MomentumThreshold {
			s.logger.Debug("Momentum detected: %.2f%% move in %s",
				priceChange*100, token.TokenID[:20])
			s.enterPosition(token.TokenID, marketName, update.BestAsk, update)
		}
	}

	// Update last price
	s.Mu.Lock()
	s.lastPrices[token.TokenID] = update.BestAsk
	s.Mu.Unlock()
}

func (s *MomentumStrategy) enterPosition(tokenID, market string, price float64, lastUpdate client.MarketUpdate) {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	if !s.posManager.CanEnter(s.CurrentBalance, len(s.Positions), s.config.MaxPositionSize) {
		return
	}

	shares := s.config.MaxPositionSize / price

	s.logger.LogEntry(tokenID, market, price, s.config.MaxPositionSize, "Momentum signal")

	// Record signal
	signalID, err := dbops.RecordSignal(s.Ctx, s.Store, dbops.SignalParams{
		SessionID:    s.SessionID,
		TokenID:      tokenID,
		SignalType:   "buy",
		BestBid:      lastUpdate.BestBid,
		BestAsk:      lastUpdate.BestAsk,
		ActionReason: "Momentum signal",
		Confidence:   70,
	})

	if err != nil {
		s.logger.Error("Failed to record signal: %v", err)
	} else {
		s.logger.Debug("Signal %d recorded", signalID)
	}

	s.Positions[tokenID] = &strategy.Position{
		TokenID:      tokenID,
		Market:       market,
		Shares:       shares,
		EntryPrice:   price,
		CurrentPrice: price,
		EntryTime:    time.Now(),
		Metadata: map[string]interface{}{
			"spread":           common.CalculateSpread(lastUpdate.BestBid, lastUpdate.BestAsk),
			"momentumStrength": (price - s.lastPrices[tokenID]) / s.lastPrices[tokenID],
		},
	}

	s.CurrentBalance -= s.config.MaxPositionSize
}

func (s *MomentumStrategy) checkExitConditions() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	for tokenID, pos := range s.Positions {
		// Update price
		if s.config.TestMode && time.Since(pos.EntryTime) > 20*time.Second {
			pos.CurrentPrice = pos.EntryPrice * 1.12
		} else {
			book, err := s.marketFetcher.GetOrderBook(tokenID)
			if err == nil {
				if bestBid := common.ExtractBestBid(book); bestBid > 0 {
					pos.CurrentPrice = bestBid
				}
			}
		}

		// Use position manager to check exits
		params := position.ExitParams{
			CurrentPrice: pos.CurrentPrice,
			EntryPrice:   pos.EntryPrice,
			EntryTime:    pos.EntryTime,
			Metadata:     pos.Metadata,
		}

		// Check exit conditions (no max hold time check needed)
		shouldExit, reason := s.posManager.CheckExitConditions(params)

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

func (s *MomentumStrategy) exitPosition(tokenID string, price float64, reason string) {
	pos := s.Positions[tokenID]
	if pos == nil {
		return
	}

	proceeds := pos.Shares * price
	pnl, _ := s.posManager.CalculatePnLFromCost(s.config.MaxPositionSize, proceeds)

	s.logger.LogExit(tokenID, pos.EntryPrice, price, pnl, reason)
	s.perfTracker.RecordTrade(pnl)

	dbops.RecordSignal(s.Ctx, s.Store, dbops.SignalParams{
		SessionID:    s.SessionID,
		TokenID:      tokenID,
		SignalType:   "sell",
		BestBid:      price,
		BestAsk:      price,
		ActionReason: reason,
		Confidence:   90,
	})

	delete(s.Positions, tokenID)
	s.CurrentBalance += proceeds
}

func (s *MomentumStrategy) printStatus() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	totalValue := s.CurrentBalance
	for _, pos := range s.Positions {
		totalValue += pos.Shares * pos.CurrentPrice
	}

	pnl := totalValue - s.config.InitialBalance
	s.perfTracker.UpdateBalance(totalValue)

	s.logger.LogStatus(totalValue, len(s.Positions), pnl)
	dbops.UpdateSessionBalance(s.Ctx, s.Store, s.SessionID, totalValue)
}

func (s *MomentumStrategy) logPerformance() {
	s.perfTracker.LogSummary()
}

func (s *MomentumStrategy) shutdown() {
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

	// Close logger
	s.logger.Close()
}
