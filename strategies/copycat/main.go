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
	"github.com/OldEphraim/polymarket-go-connection/utils/clients"
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

type CopycatStrategy struct {
	*types.BaseStrategy
	*strategy.BaseStrategyRunner

	// Configuration
	config *config.CopycatConfig

	// Common components needed by strategy
	MarketFetcher market.Fetcher
	WSManager     *websocket.Manager
	PosManager    *position.Manager
	Logger        *logging.StrategyLogger

	// Strategy-specific components
	dataAPI     *clients.PolymarketDataAPI
	exitChecker *strategy.ExitChecker

	// Tracking
	traderActivity map[string][]clients.UserActivity
	lastCheckTime  map[string]time.Time
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

	s, err := NewCopycatStrategy(ctx, cancel, cfg, level)
	if err != nil {
		log.Fatal(err)
	}

	s.BaseStrategyRunner.Run()
}

func NewCopycatStrategy(ctx context.Context, cancel context.CancelFunc, cfg *config.CopycatConfig, logLevel logging.LogLevel) (*CopycatStrategy, error) {
	// Use factory to create common components
	initConfig := strategy.StrategyInitConfig{
		Name:            cfg.Name,
		InitialBalance:  cfg.InitialBalance,
		MaxPositions:    cfg.MaxPositions,
		MaxPositionSize: cfg.MaxPositionSize,
		StopLoss:        cfg.StopLoss,
		TakeProfit:      cfg.TakeProfit,
		ConfigJSON: json.RawMessage(fmt.Sprintf(`{
			"scale_factor": %f,
			"max_position_size": %f,
			"max_positions": %d,
			"stop_loss": %f,
			"take_profit": %f,
			"smart_traders": %d
		}`, cfg.ScaleFactor, cfg.MaxPositionSize, cfg.MaxPositions,
			cfg.StopLoss, cfg.TakeProfit, len(cfg.SmartTraders))),
	}

	components, err := strategy.NewStrategyBase(ctx, cancel, initConfig, logLevel)
	if err != nil {
		return nil, err
	}

	// Create strategy-specific components
	dataAPI := clients.NewPolymarketDataAPI()

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

	s := &CopycatStrategy{
		BaseStrategy:   components.Base,
		config:         cfg,
		MarketFetcher:  components.MarketFetcher,
		WSManager:      components.WSManager,
		PosManager:     components.PosManager,
		Logger:         components.Logger,
		dataAPI:        dataAPI,
		exitChecker:    exitChecker,
		traderActivity: make(map[string][]clients.UserActivity),
		lastCheckTime:  make(map[string]time.Time),
	}

	// Initialize last check time
	for _, trader := range cfg.SmartTraders {
		s.lastCheckTime[trader.Address] = time.Now().Add(-10 * time.Minute)
	}

	// Build task scheduler
	taskScheduler := scheduler.NewBuilder().
		AddTask("check_traders", 30*time.Second, s.checkAllTraders).
		AddTask("check_exits", 20*time.Second, s.checkExits).
		AddTask("status_update", 2*time.Minute, s.printStatusExtended).
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
	components.Logger.Info("  Scale Factor: %.2f%%", cfg.ScaleFactor*100)
	components.Logger.Info("  Following %d Smart Traders", len(cfg.SmartTraders))

	for _, trader := range cfg.SmartTraders {
		components.Logger.Info("    • %s (%s): Score %.0f, Win Rate %.1f%%",
			trader.Nickname, trader.Address[:8], trader.SmartScore, trader.WinRate)
	}

	return s, nil
}

// Implement StrategyInterface
func (s *CopycatStrategy) GetName() string                          { return s.config.Name }
func (s *CopycatStrategy) GetPositions() map[string]*types.Position { return s.Positions }
func (s *CopycatStrategy) GetCurrentBalance() float64               { return s.CurrentBalance }
func (s *CopycatStrategy) GetInitialBalance() float64               { return s.config.InitialBalance }
func (s *CopycatStrategy) Lock()                                    { s.Mu.Lock() }
func (s *CopycatStrategy) Unlock()                                  { s.Mu.Unlock() }

func (s *CopycatStrategy) Initialize() error {
	// Initial check
	s.checkAllTraders()
	return nil
}

func (s *CopycatStrategy) OnShutdown() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	s.exitChecker.ExitAllPositions(s.Positions, &s.CurrentBalance, "Strategy shutdown")

	s.Logger.Info("Total trades analyzed: %d", s.tradesAnalyzed)
	s.Logger.Info("Total trades copied: %d", s.tradesCopied)
	s.Logger.Info("Total trades skipped: %d", s.tradesSkipped)
}

func (s *CopycatStrategy) checkExits() {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	s.exitChecker.CheckExitConditions(
		s.Positions,
		&s.CurrentBalance,
		func(pos *types.Position, params types.ExitParams) (bool, string) {
			return s.checkMasterExit(pos, params)
		},
	)
}

func (s *CopycatStrategy) checkMasterExit(pos *types.Position, params types.ExitParams) (bool, string) {
	metadata := params.Metadata
	masterAddress := metadata["masterAddress"].(string)
	masterNickname := metadata["masterNickname"].(string)

	if !s.checkMasterStillIn(masterAddress, pos.TokenID) {
		return true, fmt.Sprintf("Master exited (%s)", masterNickname)
	}
	return false, ""
}

func (s *CopycatStrategy) printStatusExtended() {
	s.BaseStrategyRunner.PrintStatus()
	s.Logger.Info("  Trades Analyzed: %d", s.tradesAnalyzed)
	s.Logger.Info("  Trades Copied: %d", s.tradesCopied)
	s.Logger.Info("  Trades Skipped: %d", s.tradesSkipped)
}

// Strategy-specific methods
func (s *CopycatStrategy) checkAllTraders() {
	s.checkCount++
	s.Logger.Debug("Checking trader activity #%d", s.checkCount)

	for _, trader := range s.config.SmartTraders {
		s.checkTraderActivity(trader)
	}
}

func (s *CopycatStrategy) checkTraderActivity(trader config.TraderConfig) {
	activities, err := s.dataAPI.GetUserActivity(trader.Address, 20)
	if err != nil {
		s.Logger.Warn("Failed to check %s: %v", trader.Nickname, err)
		return
	}

	s.Mu.Lock()
	lastCheck := s.lastCheckTime[trader.Address]
	s.lastCheckTime[trader.Address] = time.Now()
	s.traderActivity[trader.Address] = activities
	s.Mu.Unlock()

	for _, activity := range activities {
		activityTime := time.Unix(activity.Timestamp, 0)

		if activityTime.Before(lastCheck) {
			continue
		}

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
	if activity.Side != "BUY" {
		s.Logger.Debug("Skipping SELL trade from %s", trader.Nickname)
		return false
	}

	if activity.Price < 0.05 || activity.Price > 0.95 {
		s.Logger.Debug("Skipping extreme price %.3f from %s", activity.Price, trader.Nickname)
		return false
	}

	tradeAge := time.Since(time.Unix(activity.Timestamp, 0))
	if tradeAge > 2*time.Minute {
		s.Logger.Debug("Trade too old: %.1f minutes from %s", tradeAge.Minutes(), trader.Nickname)
		return false
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()

	positionKey := fmt.Sprintf("%s-%s", trader.Address, activity.Asset)
	if _, exists := s.Positions[positionKey]; exists {
		s.Logger.Debug("Already have position for %s from %s", activity.Asset[:8], trader.Nickname)
		return false
	}

	desiredSize := activity.USDCSize * s.config.ScaleFactor
	ourSize := s.PosManager.CalculatePositionSize(desiredSize, 5)

	if !s.PosManager.CanEnter(s.CurrentBalance, len(s.Positions), ourSize) {
		s.Logger.Debug("Cannot enter: insufficient balance or max positions reached")
		return false
	}

	return true
}

func (s *CopycatStrategy) copyTrade(trader config.TraderConfig, activity clients.UserActivity) {
	book, err := s.MarketFetcher.GetOrderBook(activity.Asset)
	if err != nil {
		s.Logger.Warn("Failed to get orderbook for %s: %v", activity.Asset[:20], err)
		return
	}

	currentAsk := common.ExtractBestAsk(book)
	currentBid := common.ExtractBestBid(book)

	if currentAsk <= 0 || currentBid <= 0 {
		s.Logger.Warn("Invalid orderbook prices for %s: bid=%.4f, ask=%.4f",
			activity.Asset[:20], currentBid, currentAsk)
		return
	}

	spread := common.CalculateSpread(currentBid, currentAsk)
	if spread > 0.05 {
		s.Logger.Warn("Spread too wide for %s: %.2f%%", activity.Asset[:20], spread*100)
		return
	}

	priceSlippage := (currentAsk - activity.Price) / activity.Price
	if priceSlippage > 0.10 {
		s.Logger.Warn("Price slippage too high for %s: trader paid %.4f, now %.4f (%.1f%%)",
			activity.Asset[:20], activity.Price, currentAsk, priceSlippage*100)
		return
	}

	if priceSlippage < -0.20 {
		s.Logger.Warn("Price dropped significantly for %s: trader paid %.4f, now %.4f (%.1f%%)",
			activity.Asset[:20], activity.Price, currentAsk, priceSlippage*100)
		return
	}

	desiredSize := activity.USDCSize * s.config.ScaleFactor
	ourSize := s.PosManager.CalculatePositionSize(desiredSize, 5)
	shares := ourSize / currentAsk

	reason := fmt.Sprintf("Copying %s (score: %.0f)", trader.Nickname, trader.SmartScore)

	s.Logger.LogEntry(activity.Asset, activity.Title, currentAsk, ourSize, reason)
	s.Logger.Info("  Master: %s (%s)", trader.Nickname, trader.Address[:8])
	s.Logger.Info("  Master paid: $%.4f → Current ask: $%.4f (slippage: %.1f%%)",
		activity.Price, currentAsk, priceSlippage*100)
	s.Logger.Info("  Their Size: $%.2f → Our Size: $%.2f", activity.USDCSize, ourSize)
	s.Logger.Info("  Spread: %.2f%%", spread*100)

	signalID, err := dbops.RecordSignal(s.Ctx, s.Store, dbops.SignalParams{
		SessionID:    s.SessionID,
		TokenID:      activity.Asset,
		SignalType:   "buy",
		BestBid:      currentBid,
		BestAsk:      currentAsk,
		ActionReason: reason,
		Confidence:   trader.SmartScore,
	})

	if err != nil {
		s.Logger.Error("Failed to record signal: %v", err)
	} else {
		s.Logger.Debug("Signal %d recorded", signalID)
	}

	s.WSManager.MonitorPriceUpdates(activity.Asset, func(update client.MarketUpdate) {
		s.updatePositionPrice(fmt.Sprintf("%s-%s", trader.Address, activity.Asset), update)
	})

	s.Mu.Lock()
	positionKey := fmt.Sprintf("%s-%s", trader.Address, activity.Asset)
	s.Positions[positionKey] = &types.Position{
		TokenID:      activity.Asset,
		Market:       activity.Title,
		Shares:       shares,
		EntryPrice:   currentAsk,
		CurrentPrice: currentAsk,
		EntryTime:    time.Now(),
		Metadata: map[string]interface{}{
			"masterAddress":  trader.Address,
			"masterNickname": trader.Nickname,
			"masterPrice":    activity.Price,
			"masterSize":     activity.USDCSize,
			"ourSize":        ourSize,
			"slippage":       priceSlippage,
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
		priceChange := (update.BestBid - pos.CurrentPrice) / pos.CurrentPrice
		if priceChange > -0.50 && priceChange < 0.50 {
			pos.CurrentPrice = update.BestBid
			s.Logger.LogMetric(fmt.Sprintf("price_%s", posKey[:16]), update.BestBid)
		} else {
			s.Logger.Warn("Ignoring suspicious price update for %s: %.4f → %.4f (%.1f%% change)",
				posKey[:16], pos.CurrentPrice, update.BestBid, priceChange*100)
		}
	}
}

func (s *CopycatStrategy) checkMasterStillIn(masterAddress, assetID string) bool {
	activities, err := s.dataAPI.GetUserActivity(masterAddress, 10)
	if err != nil {
		s.Logger.Warn("Failed to check master position: %v", err)
		return true
	}

	for _, activity := range activities {
		if activity.Asset == assetID && activity.Side == "SELL" {
			if time.Since(time.Unix(activity.Timestamp, 0)) < 1*time.Hour {
				return false
			}
		}
	}
	return true
}
