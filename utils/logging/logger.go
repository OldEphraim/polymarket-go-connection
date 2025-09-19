package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	CRITICAL
)

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case CRITICAL:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// StrategyLogger provides structured logging for trading strategies
type StrategyLogger struct {
	strategyName  string
	logLevel      LogLevel
	fileLogger    *log.Logger
	consoleLogger *log.Logger
	logFile       *os.File
	mu            sync.Mutex

	// Metrics tracking
	entriesLogged int
	exitsLogged   int
	errorsLogged  int
}

// NewStrategyLogger creates a new strategy logger
func NewStrategyLogger(strategyName string) (*StrategyLogger, error) {
	return NewStrategyLoggerWithLevel(strategyName, INFO)
}

// NewStrategyLoggerWithLevel creates a logger with a specific log level
func NewStrategyLoggerWithLevel(strategyName string, level LogLevel) (*StrategyLogger, error) {
	// Create logs directory
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("%s_%s.log", strategyName, timestamp)
	filepath := filepath.Join(logDir, filename)

	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	return &StrategyLogger{
		strategyName:  strategyName,
		logLevel:      level,
		fileLogger:    log.New(file, "", log.LstdFlags),
		consoleLogger: log.New(os.Stdout, "", log.LstdFlags),
		logFile:       file,
	}, nil
}

// SetLevel changes the log level
func (sl *StrategyLogger) SetLevel(level LogLevel) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.logLevel = level
}

// Close closes the log file
func (sl *StrategyLogger) Close() error {
	if sl.logFile != nil {
		return sl.logFile.Close()
	}
	return nil
}

// Log methods for different levels

func (sl *StrategyLogger) Debug(format string, args ...interface{}) {
	sl.log(DEBUG, format, args...)
}

func (sl *StrategyLogger) Info(format string, args ...interface{}) {
	sl.log(INFO, format, args...)
}

func (sl *StrategyLogger) Warn(format string, args ...interface{}) {
	sl.log(WARN, format, args...)
}

func (sl *StrategyLogger) Error(format string, args ...interface{}) {
	sl.errorsLogged++
	sl.log(ERROR, format, args...)
}

func (sl *StrategyLogger) Critical(format string, args ...interface{}) {
	sl.errorsLogged++
	sl.log(CRITICAL, format, args...)
}

// Trading-specific logging methods

// LogEntry logs a position entry
func (sl *StrategyLogger) LogEntry(tokenID, market string, price, size float64, reason string) {
	sl.mu.Lock()
	sl.entriesLogged++
	sl.mu.Unlock()

	sl.log(INFO, "=== ENTERING POSITION ===")
	sl.log(INFO, "  Token: %s", tokenID[:20])
	sl.log(INFO, "  Market: %s", market)
	sl.log(INFO, "  Price: %.4f", price)
	sl.log(INFO, "  Size: $%.2f", size)
	sl.log(INFO, "  Reason: %s", reason)
}

// LogExit logs a position exit
func (sl *StrategyLogger) LogExit(tokenID string, entryPrice, exitPrice, pnl float64, reason string) {
	sl.mu.Lock()
	sl.exitsLogged++
	sl.mu.Unlock()

	pnlPercent := (exitPrice - entryPrice) / entryPrice * 100

	sl.log(INFO, "=== EXITING POSITION ===")
	sl.log(INFO, "  Token: %s", tokenID[:20])
	sl.log(INFO, "  Entry: %.4f → Exit: %.4f", entryPrice, exitPrice)
	sl.log(INFO, "  P&L: $%.2f (%.1f%%)", pnl, pnlPercent)
	sl.log(INFO, "  Reason: %s", reason)
}

// LogStatus logs a status update
func (sl *StrategyLogger) LogStatus(balance float64, positions int, pnl float64) {
	pnlPercent := pnl / (balance - pnl) * 100

	sl.log(INFO, "=== STATUS UPDATE ===")
	sl.log(INFO, "  Balance: $%.2f", balance)
	sl.log(INFO, "  Positions: %d", positions)
	sl.log(INFO, "  P&L: $%.2f (%.1f%%)", pnl, pnlPercent)
	sl.log(INFO, "  Entries: %d, Exits: %d, Errors: %d",
		sl.entriesLogged, sl.exitsLogged, sl.errorsLogged)
}

// LogTrade logs a trade execution
func (sl *StrategyLogger) LogTrade(side string, tokenID string, price, shares float64) {
	sl.log(INFO, "TRADE: %s %.2f shares of %s @ %.4f",
		side, shares, tokenID[:20], price)
}

// LogSignal logs a trading signal
func (sl *StrategyLogger) LogSignal(signalType string, tokenID string, confidence float64, reason string) {
	sl.log(INFO, "SIGNAL: %s %s (confidence: %.0f%%) - %s",
		signalType, tokenID[:20], confidence, reason)
}

// LogMetric logs a performance metric
func (sl *StrategyLogger) LogMetric(name string, value interface{}) {
	sl.log(DEBUG, "METRIC: %s = %v", name, value)
}

// Private methods

func (sl *StrategyLogger) log(level LogLevel, format string, args ...interface{}) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	// Check if we should log this level
	if level < sl.logLevel {
		return
	}

	// Format message
	prefix := fmt.Sprintf("[%s][%s] ", sl.strategyName, level)
	message := fmt.Sprintf(format, args...)
	fullMessage := prefix + message

	// Log to file
	if sl.fileLogger != nil {
		sl.fileLogger.Println(fullMessage)
	}

	// Log to console with color coding
	if sl.consoleLogger != nil {
		coloredMessage := sl.colorize(level, fullMessage)
		sl.consoleLogger.Println(coloredMessage)
	}
}

func (sl *StrategyLogger) colorize(level LogLevel, message string) string {
	// ANSI color codes
	const (
		colorReset  = "\033[0m"
		colorRed    = "\033[31m"
		colorGreen  = "\033[32m"
		colorYellow = "\033[33m"
		colorBlue   = "\033[34m"
		colorPurple = "\033[35m"
		colorCyan   = "\033[36m"
		colorGray   = "\033[90m"
	)

	switch level {
	case DEBUG:
		return colorGray + message + colorReset
	case INFO:
		return colorCyan + message + colorReset
	case WARN:
		return colorYellow + message + colorReset
	case ERROR:
		return colorRed + message + colorReset
	case CRITICAL:
		return colorPurple + message + colorReset
	default:
		return message
	}
}

// GlobalLogger is a singleton logger for general use
var (
	globalLogger *StrategyLogger
	globalMu     sync.Mutex
)

// InitGlobalLogger initializes the global logger
func InitGlobalLogger(strategyName string) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	logger, err := NewStrategyLogger(strategyName)
	if err != nil {
		return err
	}

	globalLogger = logger
	return nil
}

// GetLogger returns the global logger
func GetLogger() *StrategyLogger {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalLogger == nil {
		// Create a default logger
		globalLogger, _ = NewStrategyLogger("default")
	}

	return globalLogger
}

// Convenience functions for global logging

func Debug(format string, args ...interface{}) {
	GetLogger().Debug(format, args...)
}

func Info(format string, args ...interface{}) {
	GetLogger().Info(format, args...)
}

func Warn(format string, args ...interface{}) {
	GetLogger().Warn(format, args...)
}

func Error(format string, args ...interface{}) {
	GetLogger().Error(format, args...)
}

func Critical(format string, args ...interface{}) {
	GetLogger().Critical(format, args...)
}

// PerformanceTracker tracks strategy performance over time
type PerformanceTracker struct {
	logger        *StrategyLogger
	startTime     time.Time
	trades        int
	winningTrades int
	losingTrades  int
	totalPnL      float64
	maxDrawdown   float64
	peakBalance   float64
}

// NewPerformanceTracker creates a new performance tracker
func NewPerformanceTracker(logger *StrategyLogger) *PerformanceTracker {
	return &PerformanceTracker{
		logger:      logger,
		startTime:   time.Now(),
		peakBalance: 0,
	}
}

// RecordTrade records a completed trade
func (pt *PerformanceTracker) RecordTrade(pnl float64) {
	pt.trades++
	pt.totalPnL += pnl

	if pnl > 0 {
		pt.winningTrades++
	} else if pnl < 0 {
		pt.losingTrades++
	}
}

// UpdateBalance updates the current balance and tracks drawdown
func (pt *PerformanceTracker) UpdateBalance(balance float64) {
	if balance > pt.peakBalance {
		pt.peakBalance = balance
	}

	drawdown := (pt.peakBalance - balance) / pt.peakBalance
	if drawdown > pt.maxDrawdown {
		pt.maxDrawdown = drawdown
	}
}

// LogSummary logs a performance summary
func (pt *PerformanceTracker) LogSummary() {
	runtime := time.Since(pt.startTime)
	winRate := float64(pt.winningTrades) / float64(pt.trades) * 100

	pt.logger.Info("=== PERFORMANCE SUMMARY ===")
	pt.logger.Info("  Runtime: %v", runtime)
	pt.logger.Info("  Total Trades: %d", pt.trades)
	pt.logger.Info("  Win Rate: %.1f%%", winRate)
	pt.logger.Info("  Total P&L: $%.2f", pt.totalPnL)
	pt.logger.Info("  Max Drawdown: %.1f%%", pt.maxDrawdown*100)
	pt.logger.Info("  Avg P&L per Trade: $%.2f", pt.totalPnL/float64(pt.trades))
}
