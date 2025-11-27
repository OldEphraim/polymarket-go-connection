package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/gatherer"
	"github.com/OldEphraim/polymarket-go-connection/utils/gatherer_client"
	"github.com/OldEphraim/polymarket-go-connection/utils/paper_trading"
	"github.com/OldEphraim/polymarket-go-connection/utils/strategy_persistence"
	"github.com/joho/godotenv"
)

// ===== Helper: truncate for logs =====
func truncateID(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length]
}

// ===== Configuration =====
type MeanReversionConfig struct {
	Name               string  `json:"name"`
	Duration           string  `json:"duration"`
	ReversionThreshold float64 `json:"reversion_threshold"` // legacy fallback by distance from 0.5
	HoldPeriod         string  `json:"hold_period"`

	// Feature-driven gates
	MaxSpreadBps int     `json:"max_spread_bps"`
	MinAbsZ      float64 `json:"min_abs_z"`
}

func loadConfig(filename string) *MeanReversionConfig {
	cfg := &MeanReversionConfig{
		Name:               "mean_reversion",
		Duration:           "24h",
		ReversionThreshold: 0.30,
		HoldPeriod:         "2h",
		MaxSpreadBps:       120,
		MinAbsZ:            2.5,
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
	_ = godotenv.Load()

	// ===== STEP 3: Load Configuration =====
	cfg := loadConfig(*configFile)
	log.Printf("Config loaded: thresh=%.2f, hold=%s, gates={max_spread_bps=%d,min_abs_z=%.2f}",
		cfg.ReversionThreshold, cfg.HoldPeriod, cfg.MaxSpreadBps, cfg.MinAbsZ)

	// ===== STEP 4: Create Context =====
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if cfg.Duration != "" && cfg.Duration != "infinite" {
		if duration, err := time.ParseDuration(cfg.Duration); err == nil {
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

	sqlDB, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()

	// Ensure strategy + open session (0 start to be bankroll-agnostic)
	sess, err := strategy_persistence.EnsureOpenSession(ctx, sqlDB, "mean_reversion", 0)
	if err != nil {
		log.Fatalf("EnsureOpenSession: %v", err)
	}
	log.Printf("Using session id=%d", sess.ID)

	// ===== STEP 6: Initialize Paper Trading Framework (unlimited funds) =====
	trader := paper_trading.New(store, paper_trading.Config{
		UnlimitedFunds: true,
		InitialBalance: 0,
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
	reversionHits := 0
	positionsEntered := 0

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				log.Printf("STATUS: Events=%d, ReversionHits=%d, Positions=%d",
					eventsProcessed, reversionHits, positionsEntered)
			}
		}
	}()

	// ===== STEP 9: Main Event Processing Loop =====
	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled, shutting down...")
			for tokenID, pos := range positions {
				if pos.Status == "open" {
					exitPx := pos.CurrentPrice
					if exitPx <= 0 {
						exitPx = pos.EntryPrice
					}
					// Persist exit + credit cash
					_ = strategy_persistence.RecordExit(ctx, sqlDB, strategy_persistence.ExitParams{
						SessionID: sess.ID,
						TokenID:   pos.TokenID,
						ExitPrice: exitPx,
						ExitSize:  0,
						Reason:    "Strategy shutdown",
						SideHint:  pos.Side,
					})
					_ = strategy_persistence.CreditSession(ctx, sqlDB, sess.ID, pos.Size*exitPx)

					_, _ = trader.ExitPosition(ctx, tokenID, exitPx, "Strategy shutdown")
				}
			}
			_ = strategy_persistence.EndSession(ctx, sqlDB, sess.ID)
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

			// ===== STEP 10: Filter for reversion-eligible events =====
			if event.Type != gatherer.StateExtreme {
				continue
			}

			z, hasZ := getFloat(event.Metadata, "zscore_5m")
			spreadBps, hasSpread := getFloat(event.Metadata, "spread_bps")

			var passes bool
			if hasZ && hasSpread {
				if abs(z) >= cfg.MinAbsZ && int(spreadBps) <= cfg.MaxSpreadBps {
					passes = true
				}
			} else {
				// fallback: extreme distance from 0.5
				px := event.NewValue
				if px <= 0 {
					if mid, ok := latestMidOrTrade(ctx, sqlDB, event.TokenID); ok {
						px = mid
					}
				}
				if px > 0 && abs(px-0.5) >= cfg.ReversionThreshold {
					passes = true
				}
			}
			if !passes {
				continue
			}
			reversionHits++

			// ===== STEP 12: Skip if already in position =====
			if p, exists := positions[event.TokenID]; exists && p.Status == "open" {
				continue
			}

			// ===== STEP 13: Determine current price =====
			price := event.NewValue
			if price <= 0 {
				if mid, ok := latestMidOrTrade(ctx, sqlDB, event.TokenID); ok {
					price = mid
				} else {
					log.Printf("Skipping %s — no price available", truncateID(event.TokenID, 8))
					continue
				}
			}

			// ===== STEP 14: Decide side (bet against the extreme) =====
			side := "YES"
			if price > 0.5 {
				side = "NO"
			}

			// ===== STEP 15: Size (NOTIONAL $) -> convert to shares =====
			dist := abs(price - 0.5)
			sizeNotional := sizeFromExtremeness(dist, abs(z), hasZ)
			if sizeNotional <= 0 {
				continue
			}
			shares := sizeNotional / price

			reason := "MeanReversion"
			if hasZ {
				reason += fmt.Sprintf(" z=%.2f", z)
			}
			if hasSpread {
				reason += fmt.Sprintf(" spread=%.0fbps", spreadBps)
			}

			position, err := trader.EnterPosition(ctx, paper_trading.Entry{
				TokenID:  event.TokenID,
				Market:   strFromMeta(event.Metadata, "question", "Unknown market"),
				Side:     side,
				Size:     shares, // shares
				Price:    price,
				Reason:   reason,
				Strategy: "mean_reversion",
			})
			if err != nil {
				log.Printf("Enter failed: %v", err)
				continue
			}

			positions[event.TokenID] = position
			positionsEntered++

			// Persist entry
			if err := strategy_persistence.RecordEntry(ctx, sqlDB, strategy_persistence.EntryParams{
				SessionID: sess.ID,
				TokenID:   event.TokenID,
				Side:      side,
				Price:     price,
				Size:      shares,
				Reason:    reason,
			}); err != nil {
				log.Printf("RecordEntry failed: %v", err)
			}

			// Cash: debit notional
			if err := strategy_persistence.DebitSession(ctx, sqlDB, sess.ID, sizeNotional); err != nil {
				log.Printf("balance debit failed: %v", err)
			}

			log.Printf("✓ ENTERED %s %s @ %.4f (shares=%.2f, notional=%.2f) — %s",
				truncateID(event.TokenID, 8), side, price, shares, sizeNotional, reason)

			// ===== STEP 16: Schedule Exit =====
			go scheduleExit(ctx, trader, sqlDB, sess.ID, position, cfg.HoldPeriod)
		}
	}
}

// ===== STEP 17: Sizing helper (returns NOTIONAL $) =====
func sizeFromExtremeness(distFromMid, absZ float64, hasZ bool) float64 {
	base := 100.0
	boost := 1.0
	switch {
	case distFromMid >= 0.45:
		boost *= 3
	case distFromMid >= 0.40:
		boost *= 2
	case distFromMid >= 0.30:
		boost *= 1.5
	}
	if hasZ {
		switch {
		case absZ >= 4.0:
			boost *= 2.0
		case absZ >= 3.0:
			boost *= 1.5
		}
	}
	return base * boost
}

// ===== STEP 18: Exit timer (uses persistence bridge) =====
func scheduleExit(ctx context.Context, trader *paper_trading.Framework, sqlDB *sql.DB, sessionID int64, position *paper_trading.Position, holdPeriod string) {
	dur, err := time.ParseDuration(holdPeriod)
	if err != nil || dur <= 0 {
		dur = 2 * time.Hour
	}
	timer := time.NewTimer(dur)
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		exitPrice := position.CurrentPrice
		if exitPrice <= 0 {
			exitPrice = position.EntryPrice
		}
		// Persist + credit cash
		_ = strategy_persistence.RecordExit(ctx, sqlDB, strategy_persistence.ExitParams{
			SessionID: sessionID,
			TokenID:   position.TokenID,
			ExitPrice: exitPrice,
			ExitSize:  0,
			Reason:    "Hold period expired",
			SideHint:  position.Side,
		})
		_ = strategy_persistence.CreditSession(ctx, sqlDB, sessionID, position.Size*exitPrice)

		if _, err := trader.ExitPosition(ctx, position.TokenID, exitPrice, "Hold period expired"); err != nil {
			log.Printf("framework exit failed: %v", err)
		} else {
			log.Printf("EXITED: %s @ %.4f", truncateID(position.TokenID, 8), exitPrice)
		}
	}
}

// ===== STEP 19: Small helpers =====
func getFloat(meta map[string]interface{}, key string) (float64, bool) {
	if v, ok := meta[key]; ok {
		switch x := v.(type) {
		case float64:
			return x, true
		case json.Number:
			if f, err := x.Float64(); err == nil {
				return f, true
			}
		case string:
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func strFromMeta(meta map[string]interface{}, key, def string) string {
	if v, ok := meta[key]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			return s
		}
	}
	return def
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func latestMidOrTrade(ctx context.Context, db *sql.DB, token string) (float64, bool) {
	var bid, ask sql.NullFloat64
	err := db.QueryRowContext(ctx, `
		SELECT best_bid, best_ask
		FROM market_quotes
		WHERE token_id = $1
		ORDER BY ts DESC
		LIMIT 1
	`, token).Scan(&bid, &ask)
	if err == nil && bid.Valid && ask.Valid && bid.Float64 > 0 && ask.Float64 > 0 {
		return (bid.Float64 + ask.Float64) / 2.0, true
	}
	var px sql.NullFloat64
	err = db.QueryRowContext(ctx, `
		SELECT price
		FROM market_trades
		WHERE token_id = $1
		ORDER BY ts DESC
		LIMIT 1
	`, token).Scan(&px)
	if err == nil && px.Valid && px.Float64 > 0 {
		return px.Float64, true
	}
	return 0, false
}
