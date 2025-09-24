package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/utils/dbops"
	"github.com/OldEphraim/polymarket-go-connection/utils/logging"
	"github.com/OldEphraim/polymarket-go-connection/utils/market"
	"github.com/OldEphraim/polymarket-go-connection/utils/position"
	"github.com/OldEphraim/polymarket-go-connection/utils/types"
	"github.com/OldEphraim/polymarket-go-connection/utils/websocket"
)

// CommonComponents holds all the commonly initialized components
type CommonComponents struct {
	Base          *types.BaseStrategy
	Logger        *logging.StrategyLogger
	PosManager    *position.Manager
	WSManager     *websocket.Manager
	MarketFetcher market.Fetcher
	PerfTracker   *logging.PerformanceTracker
}

// StrategyInitConfig holds initialization parameters
type StrategyInitConfig struct {
	Name            string
	InitialBalance  float64
	MaxPositions    int
	MaxPositionSize float64
	StopLoss        float64
	TakeProfit      float64
	ConfigJSON      json.RawMessage
}

// NewStrategyBase creates and initializes common strategy components
func NewStrategyBase(
	ctx context.Context,
	cancel context.CancelFunc,
	cfg StrategyInitConfig,
	logLevel logging.LogLevel,
) (*CommonComponents, error) {
	// Create logger
	logger, err := logging.NewStrategyLoggerWithLevel(cfg.Name, logLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	logger.Info("=== INITIALIZING %s STRATEGY ===", cfg.Name)

	// Initialize base strategy
	base := &types.BaseStrategy{
		InitialBalance: cfg.InitialBalance,
		CurrentBalance: cfg.InitialBalance,
		Positions:      make(map[string]*types.Position),
		Ctx:            ctx,
		Cancel:         cancel,
	}

	// Initialize database
	store, err := db.NewStore(dbops.GetDBConnection())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	base.Store = store // store is already *db.Store
	logger.Info("✓ Database connected")

	// Initialize strategy and session in database
	dbConfig := dbops.StrategyConfig{
		Name:           cfg.Name,
		Config:         cfg.ConfigJSON,
		InitialBalance: cfg.InitialBalance,
	}

	strategyID, sessionID, err := dbops.InitializeStrategy(ctx, store, dbConfig)
	if err != nil {
		return nil, err
	}
	base.SessionID = sessionID
	logger.Info("✓ Strategy %d, Session %d created", strategyID, sessionID)

	// Initialize Polymarket client
	pmClient, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		return nil, fmt.Errorf("failed to create Polymarket client: %w", err)
	}
	base.PMClient = pmClient

	// Initialize WebSocket client
	wsClient, err := client.NewWSClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create WebSocket client: %w", err)
	}
	base.WSClient = wsClient

	// Create position manager
	posManager := position.NewManager(
		cfg.MaxPositions,
		cfg.MaxPositionSize,
		cfg.StopLoss,
		cfg.TakeProfit,
		0, // No max hold time - using strategy duration instead
	)

	// Create WebSocket manager
	wsManager := websocket.NewManager(wsClient)

	// Create market fetcher
	marketFetcher := market.NewStandardFetcher(pmClient, wsClient)

	// Create performance tracker
	perfTracker := logging.NewPerformanceTracker(logger)

	return &CommonComponents{
		Base:          base,
		Logger:        logger,
		PosManager:    posManager,
		WSManager:     wsManager,
		MarketFetcher: marketFetcher,
		PerfTracker:   perfTracker,
	}, nil
}

// LogConfiguration logs common configuration parameters
func LogConfiguration(logger *logging.StrategyLogger, cfg StrategyInitConfig) {
	logger.Info("=== CONFIGURATION ===")
	logger.Info("  Max Positions: %d", cfg.MaxPositions)
	logger.Info("  Max Position Size: $%.2f", cfg.MaxPositionSize)
	logger.Info("  Stop Loss: %.1f%%", cfg.StopLoss*100)
	logger.Info("  Take Profit: %.1f%%", cfg.TakeProfit*100)
}
