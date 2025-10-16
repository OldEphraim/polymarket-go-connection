package main

import (
	"database/sql"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/OldEphraim/polymarket-go-connection/gatherer"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	"github.com/joho/godotenv"
)

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

	sqlDB, err := sql.Open("postgres", dbURL) // requires: _ "github.com/lib/pq"
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	// (optional but recommended) sane pool settings
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	// sqlc queries handle
	q := database.New(sqlDB)

	// ⬅️ Pass BOTH the queries and the raw *sql.DB so the COPY persister can use it
	store := gatherer.NewSQLCStore(q, sqlDB)

	// Config
	cfg := gatherer.DefaultConfig()
	cfg.ScanInterval = 30 * time.Second
	if interval := os.Getenv("GATHERER_SCAN_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			cfg.ScanInterval = d
		}
	}
	// Make sure this is true to enable WS:
	// cfg.UseWebsocket = true

	// Start
	g := gatherer.New(store, cfg, logger)
	if err := g.Start(); err != nil {
		log.Fatalf("start gatherer: %v", err)
	}

	logger.Info("Gatherer service started",
		"scan_interval", cfg.ScanInterval,
		"base_url", cfg.BaseURL)

	// Wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	logger.Info("Shutdown signal received")
	g.Stop()
	logger.Info("Gatherer service stopped")
}
