package strategy

import (
	"time"

	"github.com/OldEphraim/polymarket-go-connection/utils/dbops"
	"github.com/OldEphraim/polymarket-go-connection/utils/logging"
	"github.com/OldEphraim/polymarket-go-connection/utils/position"
	"github.com/OldEphraim/polymarket-go-connection/utils/scheduler"
	"github.com/OldEphraim/polymarket-go-connection/utils/types"
	"github.com/OldEphraim/polymarket-go-connection/utils/websocket"
)

// StrategyInterface defines what each strategy must implement
type StrategyInterface interface {
	Initialize() error
	OnShutdown()
	GetName() string
	GetPositions() map[string]*types.Position
	GetCurrentBalance() float64
	GetInitialBalance() float64
	Lock()
	Unlock()
}

// BaseStrategyRunner handles common strategy execution flow
type BaseStrategyRunner struct {
	Strategy      StrategyInterface
	Base          *types.BaseStrategy
	TaskScheduler *scheduler.Scheduler
	WSManager     *websocket.Manager
	Logger        *logging.StrategyLogger
	PerfTracker   *logging.PerformanceTracker
	PosManager    *position.Manager
	StartTime     time.Time
}

// NewStrategyRunner creates a new runner with common components
func NewStrategyRunner(
	strategy StrategyInterface,
	base *types.BaseStrategy,
	scheduler *scheduler.Scheduler,
	wsManager *websocket.Manager,
	logger *logging.StrategyLogger,
	perfTracker *logging.PerformanceTracker,
	posManager *position.Manager,
) *BaseStrategyRunner {
	return &BaseStrategyRunner{
		Strategy:      strategy,
		Base:          base,
		TaskScheduler: scheduler,
		WSManager:     wsManager,
		Logger:        logger,
		PerfTracker:   perfTracker,
		PosManager:    posManager,
		StartTime:     time.Now(),
	}
}

// Run executes the strategy lifecycle
func (r *BaseStrategyRunner) Run() {
	r.Logger.Info("=== STARTING STRATEGY COMPONENTS ===")

	// Start WebSocket manager
	go r.WSManager.MaintainConnection(r.Base.Ctx)
	go r.Base.WSClient.Listen(r.Base.Ctx)

	// Wait for connections to establish
	time.Sleep(2 * time.Second)

	// Initialize strategy-specific components
	if err := r.Strategy.Initialize(); err != nil {
		r.Logger.Error("Failed to initialize strategy: %v", err)
		r.Shutdown()
		return
	}

	// Run scheduler (blocks until context done)
	r.TaskScheduler.Run(r.Base.Ctx)

	// Cleanup on exit
	r.Shutdown()
}

// PrintStatus prints common status information
func (r *BaseStrategyRunner) PrintStatus() {
	r.Strategy.Lock()
	defer r.Strategy.Unlock()

	totalValue := r.Strategy.GetCurrentBalance()
	positions := r.Strategy.GetPositions()

	for _, pos := range positions {
		totalValue += pos.Shares * pos.CurrentPrice
	}

	pnl := totalValue - r.Strategy.GetInitialBalance()
	r.PerfTracker.UpdateBalance(totalValue)

	r.Logger.LogStatus(totalValue, len(positions), pnl)
	r.Logger.Info("  Runtime: %v", time.Since(r.StartTime).Round(time.Second))

	dbops.UpdateSessionBalance(r.Base.Ctx, r.Base.Store, r.Base.SessionID, totalValue)
}

// LogPerformance logs performance metrics
func (r *BaseStrategyRunner) LogPerformance() {
	r.PerfTracker.LogSummary()
}

// Shutdown handles graceful shutdown
func (r *BaseStrategyRunner) Shutdown() {
	r.Logger.Info("=== SHUTTING DOWN ===")

	// Let strategy do its cleanup
	r.Strategy.OnShutdown()

	// End database session
	dbops.EndSession(r.Base.Ctx, r.Base.Store, r.Base.SessionID)

	// Log final performance
	r.PerfTracker.LogSummary()
	r.Logger.Info("Total runtime: %v", time.Since(r.StartTime))

	// Close logger
	r.Logger.Close()
}
