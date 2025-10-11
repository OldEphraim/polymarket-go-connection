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
type MomentumConfig struct {
	Name              string  `json:"name"`
	Duration          string  `json:"duration"`
	MomentumThreshold float64 `json:"momentum_threshold"` // legacy fallback: percent_change threshold (0.05 = 5%)
	HoldPeriod        string  `json:"hold_period"`

	// NEW: feature-driven gates (safe defaults keep behavior sane if missing from config)
	MaxSpreadBps int     `json:"max_spread_bps"` // ignore when spread wider than this
	MinVolSurge  float64 `json:"min_vol_surge"`  // vol_1m_over_5m minimum
	MinAbsRet1m  float64 `json:"min_abs_ret_1m"` // absolute |ret_1m| minimum (e.g. 0.01 = 1%)
}

func loadConfig(filename string) *MomentumConfig {
	cfg := &MomentumConfig{
		Name:              "momentum",
		Duration:          "24h",
		MomentumThreshold: 0.05, // 5% legacy jump
		HoldPeriod:        "30m",

		MaxSpreadBps: 120,
		MinVolSurge:  2.0,
		MinAbsRet1m:  0.01,
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
	configFile := flag.String("config", "momentum.json", "Config file")
	flag.Parse()

	log.Println("=== MOMENTUM STRATEGY STARTING ===")

	// ===== STEP 2: Load Environment Variables =====
	godotenv.Load()

	// ===== STEP 3: Load Configuration =====
	cfg := loadConfig(*configFile)
	log.Printf("Config loaded: legacy_threshold=%.1f%%, hold=%s, gates={max_spread_bps=%d,min_vol_surge=%.2f,min_abs_ret_1m=%.3f}",
		cfg.MomentumThreshold*100, cfg.HoldPeriod, cfg.MaxSpreadBps, cfg.MinVolSurge, cfg.MinAbsRet1m)

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

	// ALSO open a plain sql.DB for quick quote/trade lookups (keeps strategy decoupled from store internals)
	sqlDB, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()

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
	momentumHits := 0
	positionsEntered := 0

	// Status ticker
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				log.Printf("STATUS: Events=%d, MomentumHits=%d, Positions=%d",
					eventsProcessed, momentumHits, positionsEntered)
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

			// ===== STEP 10: Filter for momentum-eligible events =====
			if event.Type != gatherer.PriceJump {
				continue
			}

			// Momentum events we care about (feature-driven) carry vol_1m_over_5m, ret_1m, spread_bps.
			// If those are missing, we fall back to legacy percent_change.
			volX, hasVolX := getFloat(event.Metadata, "vol_1m_over_5m")
			ret1m, hasRet1m := getFloat(event.Metadata, "ret_1m")
			spreadBps, hasSpread := getFloat(event.Metadata, "spread_bps")

			var passes bool
			var dir string // "YES" for up, "NO" for down

			if hasVolX && hasRet1m && hasSpread {
				// ===== STEP 11: Apply feature gates (spread + volume surge + move direction) =====
				if int(spreadBps) <= cfg.MaxSpreadBps && volX >= cfg.MinVolSurge && abs(ret1m) >= cfg.MinAbsRet1m {
					passes = true
					if ret1m > 0 {
						dir = "YES"
					} else {
						dir = "NO"
					}
				}
			} else {
				// ===== STEP 11 (fallback): legacy percent_change threshold =====
				percentChange, ok := getFloat(event.Metadata, "percent_change")
				if ok && (percentChange/100.0) >= cfg.MomentumThreshold {
					passes = true
					// Direction from legacy change
					if percentChange >= 0 {
						dir = "YES"
					} else {
						dir = "NO"
					}
				}
			}

			if !passes {
				continue
			}
			momentumHits++

			// ===== STEP 12: Skip if already in position =====
			if _, exists := positions[event.TokenID]; exists {
				continue
			}

			// ===== STEP 13: Determine entry price =====
			price := event.NewValue
			if price <= 0 {
				// For feature-driven events, NewValue is often 0 — fetch a live price.
				if mid, ok := latestMidOrTrade(ctx, sqlDB, event.TokenID); ok {
					price = mid
				} else {
					log.Printf("Skipping %s — no price available", truncateID(event.TokenID, 8))
					continue
				}
			}

			// ===== STEP 14: Size based on strength =====
			size := sizeFromMomentum(hasVolX, volX, hasRet1m, ret1m)

			// ===== STEP 15: Compose reason & enter =====
			reason := "Momentum"
			if hasVolX {
				reason += fmt.Sprintf(" volx=%.2f", volX)
			}
			if hasRet1m {
				reason += fmt.Sprintf(" ret1m=%.3f", ret1m)
			}
			if hasSpread {
				reason += fmt.Sprintf(" spread=%.0fbps", spreadBps)
			}

			position, err := trader.EnterPosition(ctx, paper_trading.Entry{
				TokenID:  event.TokenID,
				Market:   strFromMeta(event.Metadata, "question", "Unknown market"),
				Side:     dir,    // YES if up, NO if down
				Size:     size,   // $
				Price:    price,  // entry price we fetched
				Reason:   reason, // audit
				Strategy: "momentum",
			})
			if err != nil {
				log.Printf("Enter failed: %v", err)
				continue
			}

			positionsEntered++
			log.Printf("✓ ENTERED %s %s at %.4f (pos#%d) — %s",
				truncateID(event.TokenID, 8), dir, price, positionsEntered, reason)

			// ===== STEP 16: Schedule Exit =====
			go scheduleExit(ctx, trader, position, cfg.HoldPeriod)
		}
	}
}

// ===== STEP 17: Sizing helper =====
func sizeFromMomentum(hasVolX bool, volX float64, hasRet1m bool, ret1m float64) float64 {
	base := 100.0
	boost := 1.0
	if hasVolX {
		switch {
		case volX >= 4:
			boost *= 3
		case volX >= 3:
			boost *= 2
		case volX >= 2:
			boost *= 1.5
		}
	}
	if hasRet1m {
		r := abs(ret1m)
		switch {
		case r >= 0.05:
			boost *= 2
		case r >= 0.02:
			boost *= 1.5
		}
	}
	return base * boost
}

// ===== STEP 18: Exit timer =====
func scheduleExit(ctx context.Context, trader *paper_trading.Framework, position *paper_trading.Position, holdPeriod string) {
	dur, err := time.ParseDuration(holdPeriod)
	if err != nil || dur <= 0 {
		dur = 30 * time.Minute
	}
	timer := time.NewTimer(dur)
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		// simple take-profit placeholder (paper)
		exitPrice := position.CurrentPrice
		if exitPrice <= 0 {
			exitPrice = position.EntryPrice
		}
		_, err := trader.ExitPosition(ctx, position.TokenID, exitPrice, "Hold period expired")
		if err != nil {
			log.Printf("Failed to exit %s: %v", truncateID(position.TokenID, 8), err)
		} else {
			log.Printf("EXITED: %s at %.4f", truncateID(position.TokenID, 8), exitPrice)
		}
	}
}

// ===== STEP 19: Small helpers (shared pattern with MR) =====
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
			if f, err := strconvParseFloat(x); err == nil {
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

// ===== STEP 20: Price resolution helpers =====
func latestMidOrTrade(ctx context.Context, db *sql.DB, token string) (float64, bool) {
	// Try mid from latest quote
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
	// fallback to last trade price
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

// parse string to float64 (no import clutter up top)
func strconvParseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
