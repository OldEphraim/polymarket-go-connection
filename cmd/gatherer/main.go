package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/OldEphraim/polymarket-go-connection/gatherer"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	"github.com/joho/godotenv"
)

type retentionPolicy struct {
	PauseGatherer      bool `json:"pause_gatherer"`
	BackpressureFreeMB int  `json:"backpressure_free_mb"` // available if you want it later
}

func readPolicy(path string) (retentionPolicy, error) {
	var p retentionPolicy
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if len(b) == 0 {
		return p, nil
	}
	err = json.Unmarshal(b, &p)
	return p, err
}

func main() {
	// Load env
	_ = godotenv.Load()

	// Logger
	logLevel := slog.LevelInfo
	if os.Getenv("DEBUG") == "true" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	// DB
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable not set")
	}

	sqlDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	// (optional but recommended) sane pool settings
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	// sqlc queries handle
	q := database.New(sqlDB)

	// Store for gatherer
	store := gatherer.NewSQLCStore(q, sqlDB)

	// Config
	cfg := gatherer.DefaultConfig()
	cfg.ScanInterval = 30 * time.Second
	if interval := os.Getenv("GATHERER_SCAN_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			cfg.ScanInterval = d
		}
	}
	// If you have a debug server, keep it as-is (cfg.UseWebsocket etc.)

	// Gatherer instance
	g := gatherer.New(store, cfg, logger)

	// Lifecycle management
	var mu sync.Mutex
	running := false
	start := func() {
		mu.Lock()
		defer mu.Unlock()
		if running {
			return
		}
		if err := g.Start(); err != nil {
			logger.Error("start gatherer failed", "err", err)
			return
		}
		running = true
		logger.Info("Gatherer started",
			"scan_interval", cfg.ScanInterval,
			"base_url", cfg.BaseURL)
	}
	stop := func() {
		mu.Lock()
		defer mu.Unlock()
		if !running {
			return
		}
		g.Stop()
		running = false
		logger.Info("Gatherer stopped")
	}

	// Start initially unless paused by policy
	policyPath := "/tmp/retention_policy.json"
	initialPolicy, _ := readPolicy(policyPath)
	if initialPolicy.PauseGatherer {
		logger.Warn("Gatherer paused by policy at startup")
	} else {
		start()
	}

	// Watch policy and honor PauseGatherer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()

		pausedLast := initialPolicy.PauseGatherer
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p, err := readPolicy(policyPath)
				if err != nil {
					// Not fatal—just log and continue
					logger.Debug("policy read error", "err", err)
					continue
				}
				paused := p.PauseGatherer
				if paused != pausedLast {
					pausedLast = paused
					if paused {
						logger.Warn("Policy set PauseGatherer=true; stopping gatherer")
						stop()
					} else {
						logger.Info("Policy cleared PauseGatherer; starting gatherer")
						start()
					}
				}
			}
		}
	}()

	// Wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	logger.Info("Shutdown signal received")
	stop()
	logger.Info("Gatherer service exited")
}
