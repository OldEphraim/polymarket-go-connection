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
	"github.com/OldEphraim/polymarket-go-connection/utils/clients"
	"github.com/OldEphraim/polymarket-go-connection/utils/config"
	"github.com/OldEphraim/polymarket-go-connection/utils/dbops"
	"github.com/OldEphraim/polymarket-go-connection/utils/logging"
	"github.com/OldEphraim/polymarket-go-connection/utils/scheduler"
	"github.com/OldEphraim/polymarket-go-connection/utils/strategy"
	"github.com/OldEphraim/polymarket-go-connection/utils/types"
	"github.com/joho/godotenv"
)

type WhaleFollowStrategy struct {
	*types.BaseStrategy
	*strategy.BaseStrategyRunner

	// Configuration
	config *config.WhaleConfig

	// API clients
	hashdiveAPI *clients.HashdiveAPI
	exitChecker *strategy.ExitChecker

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

	s, err := NewWhaleFollowStrategy(ctx, cancel, cfg, level, apiKey)
	if err != nil {
		log.Fatal(err)
	}

	s.BaseStrategyRunner.Run()
}

func NewWhaleFollowStrategy(ctx context.Context, cancel context.CancelFunc, cfg *config.WhaleConfig, logLevel logging.LogLevel, apiKey string) (*WhaleFollowStrategy, error) {
	// Use factory to create common components
	initConfig := strategy.StrategyInitConfig{
		Name:            cfg.Name,
		InitialBalance:  cfg.InitialBalance,
		MaxPositions:    cfg.MaxPositions,
		MaxPositionSize: cfg.MaxPositionSize,
		StopLoss:        cfg.StopLoss,
		TakeProfit:      cfg.TakeProfit,
		ConfigJSON: json.RawMessage(fmt.Sprintf(`{
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
	}

	components, err := strategy.NewStrategyBase(ctx, cancel, initConfig, logLevel)
	if err != nil {
		return nil, err
	}

	// Create Hashdive API client
	hashdiveAPI := clients.NewHashdiveAPI(apiKey)

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

	s := &WhaleFollowStrategy{
		BaseStrategy: components.Base,
		config:       cfg,
		hashdiveAPI:  hashdiveAPI,
		exitChecker:  exitChecker,
		whaleCache:   make(map[string]time.Time),
	}

	// Build task scheduler
	taskScheduler := scheduler.NewBuilder().
		AddTask("check_whales", 1*time.Minute, s.checkWhales).
		AddTask("check_exits", 30*time.Second, s.checkExits).
		AddTask("status_update", 2*time.Minute, s.printStatusExtended).
		AddTask("performance_log", 5*time.Minute, components.PerfTracker.LogSummary).
		AddTask("cache_cleanup", 1*time.Hour, s.cleanWhaleCache).
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
	components.Logger.Info("  Min Whale Size: $%.0f", cfg.MinWhaleSize)
	components.Logger.Info("  Follow Percent: %.2f%%", cfg.FollowPercent*100)
	components.Logger.Info("  Whale Exit Trigger: %.0f%%", cfg.WhaleExitTrigger*100)

	return s, nil
}

// Implement StrategyInterface
func (s *WhaleFollowStrategy) GetName() string                          { return s.config.Name }
func (s *WhaleFollowStrategy) GetPositions() map[string]*types.Position { return s.Positions }
func (s *WhaleFollowStrategy) GetCurrentBalance() float64               { return s.CurrentBalance }
func (s *WhaleFollowStrategy) GetInitialBalance() float64               { return s.config.InitialBalance }
func (s *WhaleFollowStrategy) Lock()                                    { s.Mu.Lock() }
func (s *WhaleFollowStrategy) Unlock()                                  { s.Mu.Unlock() }

func (s *WhaleFollowStrategy) Initialize() error {
	// Initial whale check
	s.checkWhales()
	return nil
}

func (s *WhaleFollowStrategy) OnShutdown() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	s.exitChecker.ExitAllPositions(s.Positions, &s.CurrentBalance, "Strategy shutdown")

	s.Logger.Info("Total whales analyzed: %d", s.tradesSeen)
	s.Logger.Info("Total checks performed: %d", s.checkCount)
}

func (s *WhaleFollowStrategy) checkExits() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	s.exitChecker.CheckExitConditions(
		s.Positions,
		&s.CurrentBalance,
		func(pos *types.Position, params types.ExitParams) (bool, string) {
			return s.checkWhaleExit(pos, params)
		},
	)
}

func (s *WhaleFollowStrategy) checkWhaleExit(pos *types.Position, params types.ExitParams) (bool, string) {
	metadata := params.Metadata
	whaleAddress := metadata["whaleAddress"].(string)
	whaleInitialSize := metadata["whaleInitialAmount"].(float64)

	// Check if whale has exited
	whalePos, err := s.hashdiveAPI.GetWhalePosition(whaleAddress, pos.TokenID)
	if err != nil {
		s.Logger.Warn("Failed to check whale position: %v", err)
		return false, ""
	}

	if whalePos != nil && whalePos.Amount < whaleInitialSize*s.config.WhaleExitTrigger {
		reduction := (1 - whalePos.Amount/whaleInitialSize) * 100
		return true, fmt.Sprintf("Whale reduced position by %.0f%%", reduction)
	}

	return false, ""
}

func (s *WhaleFollowStrategy) printStatusExtended() {
	s.BaseStrategyRunner.PrintStatus()
	s.Logger.Info("  Whales Tracked: %d", s.tradesSeen)
	s.Logger.Info("  Whale Checks: %d", s.checkCount)
	s.Logger.Info("  Cache Size: %d entries", len(s.whaleCache))
}

// Strategy-specific methods
func (s *WhaleFollowStrategy) checkWhales() {
	s.checkCount++
	s.Logger.Debug("Whale check #%d", s.checkCount)

	trades, err := s.hashdiveAPI.GetWhaleTrades(s.config.MinWhaleSize, 20)
	if err != nil {
		s.Logger.Error("Failed to fetch whale trades: %v", err)
		return
	}

	s.Logger.Debug("Received %d whale trades", len(trades))

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
		s.Logger.Info("Analyzed %d new whale trades", newTrades)
	}
}

func (s *WhaleFollowStrategy) shouldFollow(trade clients.WhaleTrade, tradeTime time.Time) bool {
	if trade.Side != "buy" || trade.Price < 0.05 || trade.Price > 0.95 {
		s.Logger.Debug("Skipping whale trade: side=%s, price=%.3f", trade.Side, trade.Price)
		return false
	}

	if time.Since(tradeTime) > 5*time.Minute {
		s.Logger.Debug("Skipping old whale trade from %v ago", time.Since(tradeTime))
		return false
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()

	if _, exists := s.Positions[trade.AssetID]; exists {
		return false
	}

	requiredSize := trade.USDSize * s.config.FollowPercent
	if requiredSize < 10 {
		requiredSize = 10
	}

	return s.PosManager.CanEnter(s.CurrentBalance, len(s.Positions), requiredSize)
}

func (s *WhaleFollowStrategy) enterWhalePosition(whale clients.WhaleTrade) {
	desiredSize := whale.USDSize * s.config.FollowPercent
	ourSize := s.PosManager.CalculatePositionSize(desiredSize, 10)
	shares := ourSize / whale.Price

	confidence := 50.0 + (whale.USDSize/100000.0)*50.0
	if confidence > 100 {
		confidence = 100
	}

	reason := fmt.Sprintf("Following whale %s ($%.0f trade)", whale.UserAddress[:8], whale.USDSize)

	s.Logger.LogEntry(whale.AssetID, whale.MarketInfo.Question, whale.Price, ourSize, reason)
	s.Logger.Info("  Whale: %s", whale.UserAddress[:8])
	s.Logger.Info("  Whale Size: $%.0f → Our Size: $%.2f", whale.USDSize, ourSize)
	s.Logger.LogSignal("BUY", whale.AssetID, confidence, reason)

	s.WSManager.MonitorPriceUpdates(whale.AssetID, func(update client.MarketUpdate) {
		s.updatePositionPrice(whale.AssetID, update)
	})

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
		s.Logger.Error("Failed to record signal: %v", err)
	} else {
		s.Logger.Debug("Signal %d recorded", signalID)
	}

	s.Mu.Lock()
	s.Positions[whale.AssetID] = &types.Position{
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
		s.Logger.LogMetric(fmt.Sprintf("price_%s", tokenID[:16]), update.BestBid)
	}
}

func (s *WhaleFollowStrategy) cleanWhaleCache() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour)
	oldCount := len(s.whaleCache)

	for key, timestamp := range s.whaleCache {
		if timestamp.Before(cutoff) {
			delete(s.whaleCache, key)
		}
	}

	cleaned := oldCount - len(s.whaleCache)
	if cleaned > 0 {
		s.Logger.Debug("Cleaned %d expired entries from whale cache, %d remaining",
			cleaned, len(s.whaleCache))
	}
}
