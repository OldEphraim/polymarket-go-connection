package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

// BaseConfig contains common configuration for all strategies
type BaseConfig struct {
	Name            string  `json:"name"`
	Duration        string  `json:"duration"` // New field: "5m", "1h", "24h", etc.
	InitialBalance  float64 `json:"initial_balance"`
	MaxPositions    int     `json:"max_positions"`
	MaxPositionSize float64 `json:"max_position_size"`
	StopLoss        float64 `json:"stop_loss"`
	TakeProfit      float64 `json:"take_profit"`
	TestMode        bool    `json:"test_mode"`
}

// GetDuration parses the duration string and returns a time.Duration
// Special case: "infinite" returns 0, which signals no timeout
func (c *BaseConfig) GetDuration() (time.Duration, error) {
	if c.Duration == "" {
		return 24 * time.Hour, nil // Default to 24 hours
	}

	// Handle infinite duration
	if c.Duration == "infinite" || c.Duration == "unlimited" {
		return 0, nil // 0 means no timeout
	}

	return time.ParseDuration(c.Duration)
}

// IsInfinite checks if the duration is set to run indefinitely
func (c *BaseConfig) IsInfinite() bool {
	return c.Duration == "infinite" || c.Duration == "unlimited"
}

// CopycatConfig contains configuration for copycat strategy
type CopycatConfig struct {
	BaseConfig
	ScaleFactor  float64        `json:"scale_factor"`
	SmartTraders []TraderConfig `json:"smart_traders"`
}

// TraderConfig represents a trader to follow
type TraderConfig struct {
	Address    string  `json:"address"`
	Nickname   string  `json:"nickname"`
	SmartScore float64 `json:"smart_score"`
	WinRate    float64 `json:"win_rate"`
}

// MomentumConfig contains configuration for momentum strategy
type MomentumConfig struct {
	BaseConfig
	MaxSpreadPercent  float64  `json:"max_spread_percent"`
	MinVolume24h      float64  `json:"min_volume_24h"`
	SearchQueries     []string `json:"search_queries"`
	MomentumThreshold float64  `json:"momentum_threshold"`
}

// WhaleConfig contains configuration for whale follow strategy
type WhaleConfig struct {
	BaseConfig
	MinWhaleSize     float64 `json:"min_whale_size"`
	FollowPercent    float64 `json:"follow_percent"`
	WhaleExitTrigger float64 `json:"whale_exit_trigger"`
}

// Loader handles loading and managing configuration
type Loader struct {
	configDir string
	cache     map[string]interface{}
}

// NewLoader creates a new configuration loader
func NewLoader() *Loader {
	return &Loader{
		configDir: "configs",
		cache:     make(map[string]interface{}),
	}
}

// NewLoaderWithDir creates a loader with a custom config directory
func NewLoaderWithDir(dir string) *Loader {
	return &Loader{
		configDir: dir,
		cache:     make(map[string]interface{}),
	}
}

// LoadCopycatConfig loads configuration for copycat strategy
func (l *Loader) LoadCopycatConfig(filename string) (*CopycatConfig, error) {
	// Check cache
	if cached, exists := l.cache[filename]; exists {
		if config, ok := cached.(*CopycatConfig); ok {
			return config, nil
		}
	}

	var config CopycatConfig
	if err := l.loadJSON(filename, &config); err != nil {
		return nil, err
	}

	// Apply defaults if not set
	l.applyDefaults(&config.BaseConfig)

	// Cache the config
	l.cache[filename] = &config
	return &config, nil
}

// LoadMomentumConfig loads configuration for momentum strategy
func (l *Loader) LoadMomentumConfig(filename string) (*MomentumConfig, error) {
	// Check cache
	if cached, exists := l.cache[filename]; exists {
		if config, ok := cached.(*MomentumConfig); ok {
			return config, nil
		}
	}

	var config MomentumConfig
	if err := l.loadJSON(filename, &config); err != nil {
		return nil, err
	}

	// Apply defaults
	l.applyDefaults(&config.BaseConfig)

	if config.MomentumThreshold == 0 {
		config.MomentumThreshold = 0.001 // Default 0.1% movement
	}

	if len(config.SearchQueries) == 0 {
		config.SearchQueries = []string{"Trump", "election", "NFL", "bitcoin"}
	}

	// Cache the config
	l.cache[filename] = &config
	return &config, nil
}

// LoadWhaleConfig loads configuration for whale follow strategy
func (l *Loader) LoadWhaleConfig(filename string) (*WhaleConfig, error) {
	// Check cache
	if cached, exists := l.cache[filename]; exists {
		if config, ok := cached.(*WhaleConfig); ok {
			return config, nil
		}
	}

	var config WhaleConfig
	if err := l.loadJSON(filename, &config); err != nil {
		return nil, err
	}

	// Apply defaults
	l.applyDefaults(&config.BaseConfig)

	if config.MinWhaleSize == 0 {
		config.MinWhaleSize = 25000
	}

	if config.FollowPercent == 0 {
		config.FollowPercent = 0.01
	}

	if config.WhaleExitTrigger == 0 {
		config.WhaleExitTrigger = 0.50
	}

	// Cache the config
	l.cache[filename] = &config
	return &config, nil
}

// LoadCustomConfig loads a custom configuration structure
func (l *Loader) LoadCustomConfig(filename string, config interface{}) error {
	return l.loadJSON(filename, config)
}

// LoadFromEnv loads configuration overrides from environment variables
func (l *Loader) LoadFromEnv(config *BaseConfig) {
	if val := os.Getenv("STRATEGY_NAME"); val != "" {
		config.Name = val
	}

	if val := os.Getenv("STRATEGY_DURATION"); val != "" {
		config.Duration = val
	}

	if val := os.Getenv("INITIAL_BALANCE"); val != "" {
		var balance float64
		fmt.Sscanf(val, "%f", &balance)
		if balance > 0 {
			config.InitialBalance = balance
		}
	}

	if val := os.Getenv("TEST_MODE"); val == "true" {
		config.TestMode = true
	}

	if val := os.Getenv("MAX_POSITIONS"); val != "" {
		var maxPos int
		fmt.Sscanf(val, "%d", &maxPos)
		if maxPos > 0 {
			config.MaxPositions = maxPos
		}
	}
}

// SaveConfig saves a configuration to file
func (l *Loader) SaveConfig(filename string, config interface{}) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	path := l.getConfigPath(filename)

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// Clear cache for this file
	delete(l.cache, filename)

	return nil
}

// GenerateDefaultConfigs creates example configuration files
func (l *Loader) GenerateDefaultConfigs() error {
	// Copycat config
	copycatConfig := CopycatConfig{
		BaseConfig: BaseConfig{
			Name:            "copycat_strategy",
			Duration:        "24h",
			InitialBalance:  1000,
			MaxPositions:    10,
			MaxPositionSize: 50,
			StopLoss:        0.05,
			TakeProfit:      0.15,
		},
		ScaleFactor: 0.01,
		SmartTraders: []TraderConfig{
			{
				Address:    "0x8bb412c8548eebcb80a25729d500cba3cb518167",
				Nickname:   "TopWhale",
				SmartScore: 99.0,
				WinRate:    56.87,
			},
		},
	}

	// Momentum config
	momentumConfig := MomentumConfig{
		BaseConfig: BaseConfig{
			Name:            "momentum_strategy",
			Duration:        "24h",
			InitialBalance:  1000,
			MaxPositions:    10,
			MaxPositionSize: 100,
			StopLoss:        0.05,
			TakeProfit:      0.10,
		},
		MaxSpreadPercent:  0.02,
		MinVolume24h:      50000,
		SearchQueries:     []string{"Trump", "election", "NFL", "bitcoin"},
		MomentumThreshold: 0.001,
	}

	// Whale config
	whaleConfig := WhaleConfig{
		BaseConfig: BaseConfig{
			Name:            "whale_follow_strategy",
			Duration:        "24h",
			InitialBalance:  1000,
			MaxPositions:    5,
			MaxPositionSize: 100,
			StopLoss:        0.10,
			TakeProfit:      0.20,
		},
		MinWhaleSize:     25000,
		FollowPercent:    0.01,
		WhaleExitTrigger: 0.50,
	}

	// Save all configs
	if err := l.SaveConfig("copycat.json", copycatConfig); err != nil {
		return err
	}

	if err := l.SaveConfig("momentum.json", momentumConfig); err != nil {
		return err
	}

	if err := l.SaveConfig("whale.json", whaleConfig); err != nil {
		return err
	}

	return nil
}

// Private methods

func (l *Loader) loadJSON(filename string, v interface{}) error {
	path := l.getConfigPath(filename)

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	return nil
}

func (l *Loader) getConfigPath(filename string) string {
	if filepath.IsAbs(filename) {
		return filename
	}
	return filepath.Join(l.configDir, filename)
}

func (l *Loader) applyDefaults(config *BaseConfig) {
	if config.Duration == "" {
		config.Duration = "24h"
	}

	if config.InitialBalance == 0 {
		config.InitialBalance = 1000
	}

	if config.MaxPositions == 0 {
		config.MaxPositions = 10
	}

	if config.MaxPositionSize == 0 {
		config.MaxPositionSize = 100
	}

	if config.StopLoss == 0 {
		config.StopLoss = 0.05
	}

	if config.TakeProfit == 0 {
		config.TakeProfit = 0.10
	}
}
