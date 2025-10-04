package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/gatherer"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found")
	}

	// Initialize logger
	logLevel := slog.LevelInfo
	if os.Getenv("DEBUG") == "true" {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	// Connect to database
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable not set")
	}

	store, err := db.NewStore(dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Configure gatherer
	config := gatherer.DefaultConfig()
	config.ScanInterval = 30 * time.Second

	// Override from environment if set
	if interval := os.Getenv("GATHERER_SCAN_INTERVAL"); interval != "" {
		if duration, err := time.ParseDuration(interval); err == nil {
			config.ScanInterval = duration
		}
	}

	// Create and start gatherer
	g := gatherer.New(store, config, logger)
	if err := g.Start(); err != nil {
		log.Fatalf("Failed to start gatherer: %v", err)
	}

	logger.Info("Gatherer service started",
		"scan_interval", config.ScanInterval,
		"base_url", config.BaseURL)

	// Run until killed
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutdown signal received")
	g.Stop()
	logger.Info("Gatherer service stopped")
}
