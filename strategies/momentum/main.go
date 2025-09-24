package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/OldEphraim/polymarket-go-connection/utils/common"
	"github.com/OldEphraim/polymarket-go-connection/utils/config"
	"github.com/OldEphraim/polymarket-go-connection/utils/dbops"
	"github.com/OldEphraim/polymarket-go-connection/utils/logging"
	"github.com/OldEphraim/polymarket-go-connection/utils/market"
	"github.com/OldEphraim/polymarket-go-connection/utils/position"
	"github.com/OldEphraim/polymarket-go-connection/utils/scheduler"
	"github.com/OldEphraim/polymarket-go-connection/utils/strategy"
	"github.com/OldEphraim/polymarket-go-connection/utils/types"
	"github.com/OldEphraim/polymarket-go-connection/utils/websocket"
	"github.com/joho/godotenv"
)

type MomentumStrategy struct {
	*types.BaseStrategy
	*strategy.BaseStrategyRunner

	// Configuration
	config *config.MomentumConfig

	// Common components needed by strategy
	MarketFetcher market.Fetcher
	WSManager     *websocket.Manager
	PosManager    *position.Manager
	Logger        *logging.StrategyLogger

	// Strategy-specific components
	searchService *market.SearchService
	exitChecker   *strategy.ExitChecker

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

	s, err := NewMomentumStrategy(ctx, cancel, cfg, level)
	if err != nil {
		log.Fatal(err)
	}

	s.BaseStrategyRunner.Run()
}

func NewMomentumStrategy(ctx context.Context, cancel context.CancelFunc, cfg *config.MomentumConfig, logLevel logging.LogLevel) (*MomentumStrategy, error) {
	// Use factory to create common components
	initConfig := strategy.StrategyInitConfig{
		Name:            cfg.Name,
		InitialBalance:  cfg.InitialBalance,
		MaxPositions:    cfg.MaxPositions,
		MaxPositionSize: cfg.MaxPositionSize,
		StopLoss:        cfg.StopLoss,
		TakeProfit:      cfg.TakeProfit,
		ConfigJSON: json.RawMessage(fmt.Sprintf(`{
            "max_position_size": %f,
            "stop_loss": %f,
            "take_profit": %f,
            "max_spread": %f,
            "min_volume_24h": %f,
            "momentum_threshold": %f
        }`, cfg.MaxPositionSize, cfg.StopLoss, cfg.TakeProfit,
			cfg.MaxSpreadPercent, cfg.MinVolume24h, cfg.MomentumThreshold)),
	}

	components, err := strategy.NewStrategyBase(ctx, cancel, initConfig, logLevel)
	if err != nil {
		return nil, err
	}

	// Create search service with filters
	searchService := market.NewSearchService()
	searchService.SetFilters(market.SearchFilters{
		MinVolume24hr: cfg.MinVolume24h,
		ActiveOnly:    true,
		MinPrice:      0.05,
		MaxPrice:      0.95,
		MaxResults:    20,
	})

	// Create exit checker
	exitChecker := &strategy.ExitChecker{
		PosManager:    components.PosManager,
		MarketFetcher: components.MarketFetcher,
		Logger:        components.Logger,
		PerfTracker:   components.PerfTracker,
		Store:         components.Base.Store,
		SessionID:     components.Base.SessionID,
		Ctx:           ctx,
	}

	s := &MomentumStrategy{
		BaseStrategy:  components.Base,
		config:        cfg,
		MarketFetcher: components.MarketFetcher,
		WSManager:     components.WSManager,
		PosManager:    components.PosManager,
		Logger:        components.Logger,
		searchService: searchService,
		exitChecker:   exitChecker,
		lastPrices:    make(map[string]float64),
	}

	// Build task scheduler
	taskScheduler := scheduler.NewBuilder().
		AddTask("discover_markets", 30*time.Minute, s.discoverMarkets).
		AddTask("check_exits", 5*time.Second, s.checkExits).
		AddTask("status_update", 30*time.Second, s.BaseStrategyRunner.PrintStatus).
		AddTask("performance_log", 5*time.Minute, components.PerfTracker.LogSummary).
		Build()

	// Create the runner
	s.BaseStrategyRunner = strategy.NewStrategyRunner(
		s,
		components.Base,
		taskScheduler,
		components.WSManager,
		components.Logger,
		components.PerfTracker,
		components.PosManager,
	)

	// Log configuration
	strategy.LogConfiguration(components.Logger, initConfig)
	components.Logger.Info("  Min Volume: $%.0f", cfg.MinVolume24h)
	components.Logger.Info("  Max Spread: %.1f%%", cfg.MaxSpreadPercent*100)
	components.Logger.Info("  Momentum Threshold: %.2f%%", cfg.MomentumThreshold*100)
	components.Logger.Info("  Search Queries: %v", cfg.SearchQueries)

	return s, nil
}

// Implement StrategyInterface
func (s *MomentumStrategy) GetName() string                          { return s.config.Name }
func (s *MomentumStrategy) GetPositions() map[string]*types.Position { return s.Positions }
func (s *MomentumStrategy) GetCurrentBalance() float64               { return s.CurrentBalance }
func (s *MomentumStrategy) GetInitialBalance() float64               { return s.config.InitialBalance }
func (s *MomentumStrategy) Lock()                                    { s.Mu.Lock() }
func (s *MomentumStrategy) Unlock()                                  { s.Mu.Unlock() }

func (s *MomentumStrategy) Initialize() error {
	// Initial market discovery
	s.discoverMarkets()

	if s.config.TestMode {
		s.Logger.Info("TEST MODE ENABLED")
		go s.simulateMarketActivity()
	}
	return nil
}

func (s *MomentumStrategy) OnShutdown() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	s.exitChecker.ExitAllPositions(s.Positions, &s.CurrentBalance, "Strategy shutdown")
}

func (s *MomentumStrategy) checkExits() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	// For test mode, we can add custom logic
	if s.config.TestMode {
		for _, pos := range s.Positions {
			if time.Since(pos.EntryTime) > 20*time.Second {
				pos.CurrentPrice = pos.EntryPrice * 1.12
			}
		}
	}

	// Use common exit checker with no custom rules for momentum
	s.exitChecker.CheckExitConditions(
		s.Positions,
		&s.CurrentBalance,
		nil, // No custom exit logic for momentum
	)
}

// Strategy-specific methods
func (s *MomentumStrategy) simulateMarketActivity() {
	s.Logger.Debug("Starting market simulation for testing")
	time.Sleep(15 * time.Second)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-ticker.C:
			s.Mu.Lock()
			if len(s.Positions) == 0 && s.PosManager.CanEnter(s.CurrentBalance, 0, s.config.MaxPositionSize) {
				s.Logger.Debug("TEST MODE: Simulating buy conditions")
			}
			s.Mu.Unlock()
		}
	}
}

func (s *MomentumStrategy) discoverMarkets() {
	s.Logger.Info("=== DISCOVERING MARKETS ===")

	results, err := s.searchService.FindMarkets(s.config.SearchQueries)
	if err != nil {
		s.Logger.Error("Failed to search markets: %v", err)
		return
	}

	s.Logger.Info("Found %d markets matching filters", len(results))

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
			book, err := s.MarketFetcher.GetOrderBook(token.TokenID)
			if err == nil {
				bid := common.ExtractBestBid(book)
				ask := common.ExtractBestAsk(book)
				if bid > 0 && ask > 0 {
					spread := common.CalculateSpread(bid, ask)
					if spread > s.config.MaxSpreadPercent {
						s.Logger.Debug("Skipping %s - spread too wide (%.2f%%)",
							token.TokenID[:20], spread*100)
						continue
					}
				}
			}

			// Subscribe using WebSocket manager
			err = s.WSManager.Subscribe(token.TokenID, func(update client.MarketUpdate) {
				s.handleMarketUpdate(token, market.Question, update)
			})

			if err == nil {
				subscribed++
				s.lastPrices[token.TokenID] = token.Price
				s.Logger.Info("✓ Subscribed to %s @ %.3f",
					common.TruncateString(market.Question, 40), token.Price)
			}
		}
	}

	s.Logger.Info("Subscribed to %d markets", subscribed)
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
	canEnter := s.PosManager.CanEnter(s.CurrentBalance, len(s.Positions), s.config.MaxPositionSize)
	lastPrice := s.lastPrices[token.TokenID]
	s.Mu.Unlock()

	if !hasPosition && canEnter && lastPrice > 0 {
		// Calculate momentum
		priceChange := (update.BestAsk - lastPrice) / lastPrice

		if s.config.TestMode || priceChange > s.config.MomentumThreshold {
			s.Logger.Debug("Momentum detected: %.2f%% move in %s",
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

	if !s.PosManager.CanEnter(s.CurrentBalance, len(s.Positions), s.config.MaxPositionSize) {
		return
	}

	shares := s.config.MaxPositionSize / price

	s.Logger.LogEntry(tokenID, market, price, s.config.MaxPositionSize, "Momentum signal")

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
		s.Logger.Error("Failed to record signal: %v", err)
	} else {
		s.Logger.Debug("Signal %d recorded", signalID)
	}

	s.Positions[tokenID] = &types.Position{
		TokenID:      tokenID,
		Market:       market,
		Shares:       shares,
		EntryPrice:   price,
		CurrentPrice: price,
		EntryTime:    time.Now(),
		Metadata: map[string]interface{}{
			"ourSize":          s.config.MaxPositionSize,
			"spread":           common.CalculateSpread(lastUpdate.BestBid, lastUpdate.BestAsk),
			"momentumStrength": (price - s.lastPrices[tokenID]) / s.lastPrices[tokenID],
		},
	}

	s.CurrentBalance -= s.config.MaxPositionSize
}
