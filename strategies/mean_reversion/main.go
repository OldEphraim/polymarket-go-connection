package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/gatherer"
	"github.com/OldEphraim/polymarket-go-connection/utils/gatherer_client"
	"github.com/OldEphraim/polymarket-go-connection/utils/paper_trading"
	"github.com/joho/godotenv"
)

// ===== Helper function to safely truncate strings =====
func truncateID(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length]
}

// ===== Configuration Structure =====
type MeanReversionConfig struct {
	Name               string  `json:"name"`
	Duration           string  `json:"duration"`
	ReversionThreshold float64 `json:"reversion_threshold"` // How far from 0.5 before we bet on reversion
	HoldPeriod         string  `json:"hold_period"`
}

func loadConfig(filename string) *MeanReversionConfig {
	cfg := &MeanReversionConfig{
		Name:               "mean_reversion",
		Duration:           "24h",
		ReversionThreshold: 0.30, // Bet on reversion when price is 0.20 or 0.80
		HoldPeriod:         "2h", // Hold longer for reversion
	}

	data, err := ioutil.ReadFile(fmt.Sprintf("configs/%s", filename))
	if err != nil {
		log.Printf("Using default config (couldn't load %s: %v)", filename, err)
		return cfg
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		log.Printf("Error parsing config: %v, using defaults", err)
	}

	return cfg
}

func main() {
	// ===== STEP 1: Parse Command Line Flags =====
	configFile := flag.String("config", "mean_reversion.json", "Config file")
	flag.Parse()

	log.Println("=== MEAN REVERSION STRATEGY STARTING ===")

	// ===== STEP 2: Load Environment Variables =====
	godotenv.Load()

	// ===== STEP 3: Load Configuration =====
	cfg := loadConfig(*configFile)
	log.Printf("Config loaded: threshold=%.2f, hold=%s",
		cfg.ReversionThreshold, cfg.HoldPeriod)

	// ===== STEP 4: Create Context =====
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.Duration != "" && cfg.Duration != "infinite" {
		duration, err := time.ParseDuration(cfg.Duration)
		if err == nil {
			log.Printf("Will run for %s", duration)
			time.AfterFunc(duration, cancel)
		}
	}

	// ===== STEP 5: Initialize Database Connection =====
	store, err := db.NewStore(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Connected to database")

	// ===== STEP 6: Initialize Paper Trading Framework =====
	trader := paper_trading.New(store, paper_trading.Config{
		UnlimitedFunds: true,
		TrackMetrics:   true,
	})
	log.Println("Paper trading initialized")

	// ===== STEP 7: Connect to Gatherer Service =====
	gathererClient := gatherer_client.New(store)
	events := gathererClient.StreamEvents(ctx)
	log.Println("Connected to gatherer service, waiting for events...")

	// ===== STEP 8: Initialize Strategy State =====
	positions := make(map[string]*paper_trading.Position)

	eventsProcessed := 0
	extremesFound := 0
	positionsEntered := 0

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Printf("STATUS: Events=%d, Extremes=%d, Positions=%d",
					eventsProcessed, extremesFound, positionsEntered)
			}
		}
	}()

	// ===== STEP 9: Main Event Processing Loop =====
	log.Println("Starting main event loop...")
	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled, shutting down...")
			for tokenID, pos := range positions {
				if pos.Status == "open" {
					_, err := trader.ExitPosition(ctx, tokenID, pos.CurrentPrice, "Strategy shutdown")
					if err == nil {
						log.Printf("Closed position %s on shutdown", truncateID(tokenID, 8))
					}
				}
			}
			metrics := trader.GetMetrics()
			log.Printf("FINAL METRICS: %+v", metrics)
			return

		case event, ok := <-events:
			if !ok {
				log.Println("Event channel closed")
				return
			}

			eventsProcessed++
			if eventsProcessed%100 == 0 {
				log.Printf("Processed %d events...", eventsProcessed)
			}

			// ===== STEP 10: Filter for Price Jump Events (we look for extremes) =====
			if event.Type != gatherer.PriceJump {
				continue
			}

			// ===== STEP 11: Check if Price is at Extreme =====
			currentPrice := event.NewValue

			// Check if price is extreme (far from 0.5)
			distanceFromMiddle := abs(currentPrice - 0.5)
			if distanceFromMiddle < cfg.ReversionThreshold {
				continue // Not extreme enough
			}

			extremesFound++
			log.Printf("Extreme price found: Token=%s, Price=%.3f (%.1f%% from middle)",
				truncateID(event.TokenID, 8), currentPrice, distanceFromMiddle*100)

			// ===== STEP 12: Check if Already in Position =====
			if _, exists := positions[event.TokenID]; exists {
				log.Printf("  → Already in position, skipping")
				continue
			}

			// ===== STEP 13: Calculate Position Size =====
			// Size based on how extreme the price is
			size := calculatePositionSize(distanceFromMiddle)
			log.Printf("  → Entering reversion position with size $%.2f", size)

			// ===== STEP 14: Enter Position (bet AGAINST the extreme) =====
			question, _ := event.Metadata["question"].(string)
			if question == "" {
				question = "Unknown market"
			}

			// If price is high (>0.5), bet NO. If price is low (<0.5), bet YES
			side := "YES"
			if currentPrice > 0.5 {
				side = "NO"
				log.Printf("  → Price high (%.3f), betting NO for reversion", currentPrice)
			} else {
				log.Printf("  → Price low (%.3f), betting YES for reversion", currentPrice)
			}

			position, err := trader.EnterPosition(ctx, paper_trading.Entry{
				TokenID:  event.TokenID,
				Market:   question,
				Side:     side,
				Size:     size,
				Price:    event.NewValue,
				Reason:   fmt.Sprintf("Mean reversion from %.3f", currentPrice),
				Strategy: "mean_reversion",
			})
			if err != nil {
				log.Printf("  → Failed to enter: %v", err)
				continue
			}

			positionsEntered++
			log.Printf("  ✓ ENTERED: %s %s at %.4f (position #%d)",
				truncateID(event.TokenID, 8), side, event.NewValue, positionsEntered)

			// ===== STEP 15: Track Position =====
			positions[event.TokenID] = position

			// ===== STEP 16: Schedule Exit =====
			go scheduleExit(ctx, trader, position, cfg.HoldPeriod)
		}
	}
}

func calculatePositionSize(extremeness float64) float64 {
	// ===== STEP 17: Size Based on How Extreme =====
	baseSize := 100.0

	if extremeness > 0.45 { // Price is 0.05 or 0.95
		return baseSize * 3
	} else if extremeness > 0.40 { // Price is 0.10 or 0.90
		return baseSize * 2
	}
	return baseSize
}

func scheduleExit(ctx context.Context, trader *paper_trading.Framework, position *paper_trading.Position, holdPeriod string) {
	// ===== STEP 18: Parse Hold Period =====
	duration, _ := time.ParseDuration(holdPeriod)

	// ===== STEP 19: Wait for Hold Period =====
	timer := time.NewTimer(duration)
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		// ===== STEP 20: Exit Position =====
		// Assume partial reversion for testing
		exitPrice := position.EntryPrice
		if position.Side == "YES" {
			exitPrice = position.EntryPrice + 0.05 // Moved up 5 cents
		} else {
			exitPrice = position.EntryPrice - 0.05 // Moved down 5 cents
		}

		_, err := trader.ExitPosition(ctx, position.TokenID, exitPrice, "Hold period expired")
		if err != nil {
			log.Printf("Failed to exit %s: %v", truncateID(position.TokenID, 8), err)
		} else {
			log.Printf("EXITED: %s at %.4f", truncateID(position.TokenID, 8), exitPrice)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
