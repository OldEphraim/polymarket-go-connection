package main

import (
	"fmt"
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
	// Load environment
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found")
	}

	// Setup logger with WARN level to reduce noise
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Changed to reduce output
	}))

	// Setup database
	dbURL := "postgresql://a8garber@localhost:5432/polymarket_dev?sslmode=disable"

	store, err := db.NewStore(dbURL)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Configure gatherer - DON'T emit new markets on first run
	config := &gatherer.Config{
		BaseURL:          "https://gamma-api.polymarket.com",
		ScanInterval:     30 * time.Second,
		LogLevel:         "warn",
		EmitNewMarkets:   false, // Turn this off for initial load
		EmitPriceJumps:   true,
		EmitVolumeSpikes: true,
	}

	// Create gatherer
	g := gatherer.New(store, config, logger)

	fmt.Println("=== Polymarket Gatherer Test ===")
	fmt.Printf("Starting at %s\n", time.Now().Format("15:04:05"))
	fmt.Println("First scan will load all markets into database...")
	fmt.Println()

	// Start gatherer
	if err := g.Start(); err != nil {
		log.Fatal("Failed to start gatherer:", err)
	}

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Event counter
	eventCounts := make(map[gatherer.EventType]int)
	eventSamples := make(map[gatherer.EventType]gatherer.MarketEvent)

	// Event consumer - much quieter
	go func() {
		for event := range g.EventChannel() {
			eventCounts[event.Type]++

			// Save a sample of each type
			if _, exists := eventSamples[event.Type]; !exists {
				eventSamples[event.Type] = event
			}

			// Only print significant events
			if event.Type != gatherer.NewMarket {
				question := ""
				if q, ok := event.Metadata["question"].(string); ok {
					if len(q) > 60 {
						question = q[:60] + "..."
					} else {
						question = q
					}
				}

				fmt.Printf("[%s] %s: %.2f%% change | %s\n",
					time.Now().Format("15:04:05"),
					event.Type,
					(event.NewValue-event.OldValue)/event.OldValue*100,
					question)
			}
		}
	}()

	// Status ticker
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			fmt.Printf("\n[%s] Status: ", time.Now().Format("15:04:05"))
			for eventType, count := range eventCounts {
				fmt.Printf("%s:%d ", eventType, count)
			}
			fmt.Println()
		}
	}()

	fmt.Println("Monitoring for market events...")
	fmt.Println("(Price jumps >5%, Volume spikes >200%, Liquidity shifts >50%)")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	// Wait for interrupt or timeout
	select {
	case <-sigChan:
		fmt.Println("\nShutting down...")
	case <-time.After(2 * time.Minute): // Shorter test duration
		fmt.Println("\nTest duration reached...")
	}

	// Cleanup
	g.Stop()

	// Final report
	fmt.Println("\n=== Final Report ===")
	fmt.Printf("Total events detected: %d\n", len(eventCounts))

	for eventType, count := range eventCounts {
		fmt.Printf("\n%s: %d events\n", eventType, count)
		if sample, exists := eventSamples[eventType]; exists && eventType != gatherer.NewMarket {
			tokenDisplay := sample.TokenID
			if len(tokenDisplay) > 8 {
				tokenDisplay = tokenDisplay[:8]
			}
			fmt.Printf("  Sample: Token=%s, Change=%.4f→%.4f\n",
				tokenDisplay, sample.OldValue, sample.NewValue)
		}
	}

	fmt.Println("\nTest completed!")
}
