package gatherer

import (
	"database/sql"
	"encoding/json"
	"math"
	"time"

	"github.com/sqlc-dev/pqtype"
)

func (g *Gatherer) debounceWindow() time.Duration {
	if g.config.Thresholds.DebounceWindow > 0 {
		return g.config.Thresholds.DebounceWindow
	}
	return 30 * time.Second
}

// ===== Legacy detectors (compatible with your strategies now) =====
func (g *Gatherer) detectLegacy(new, old *MarketScanRow, market PolymarketMarket) {
	tokenID := market.ID

	// Gate out illiquid/wide books early
	if market.BestBid <= 0 || market.BestAsk <= 0 {
		return
	}
	if (market.BestAsk - market.BestBid) > g.config.Thresholds.MaxAbsSpread {
		return
	}
	if new.Liquidity.Valid && new.Liquidity.Float64 < g.config.Thresholds.MinLiquidity {
		return
	}

	// ---- Price jump (scan-to-scan) ----
	if g.config.EmitPriceJumps {
		if old.LastPrice.Valid && new.LastPrice.Valid && old.LastPrice.Float64 > 0 {
			oldp, newp := old.LastPrice.Float64, new.LastPrice.Float64
			dp := (newp - oldp) / oldp
			absMove := math.Abs(newp - oldp)

			if math.Abs(dp) >= g.config.Thresholds.PriceJumpMinPct &&
				absMove >= g.config.Thresholds.PriceJumpMinAbs {

				// debounce per token/type (separate, longer window for price jumps)
				if g.shouldDebounce(PriceJump, tokenID, g.config.Thresholds.PriceJumpDebounce) {
					return
				}
				g.emitEvent(MarketEvent{
					Type:      PriceJump,
					TokenID:   tokenID,
					EventID:   new.EventID.String,
					Timestamp: time.Now(),
					OldValue:  oldp,
					NewValue:  newp,
					Metadata: map[string]interface{}{
						"percent_change": dp * 100,
						"abs_spread":     market.BestAsk - market.BestBid,
						"question":       market.Question,
					},
				})
			}
		}
	}

	// ---- Volume spike (>2x) ----
	if g.config.EmitVolumeSpikes {
		if old.LastVolume.Valid && new.LastVolume.Valid && old.LastVolume.Float64 > 0 &&
			new.LastVolume.Float64 > old.LastVolume.Float64*2 {

			if g.shouldDebounce(VolumeSpike, tokenID, g.config.Thresholds.DebounceWindow) {
				return
			}
			g.emitEvent(MarketEvent{
				Type:      VolumeSpike,
				TokenID:   tokenID,
				EventID:   new.EventID.String,
				Timestamp: time.Now(),
				OldValue:  old.LastVolume.Float64,
				NewValue:  new.LastVolume.Float64,
				Metadata: map[string]interface{}{
					"multiplier": new.LastVolume.Float64 / old.LastVolume.Float64,
					"question":   market.Question,
				},
			})
		}
	}

	// ---- Liquidity shift (>50%) ----
	if old.Liquidity.Valid && new.Liquidity.Valid && old.Liquidity.Float64 > 0 {
		liqChange := (new.Liquidity.Float64 - old.Liquidity.Float64) / old.Liquidity.Float64
		if math.Abs(liqChange) > 0.5 {
			if g.shouldDebounce(LiquidityShift, tokenID, g.config.Thresholds.DebounceWindow) {
				return
			}
			g.emitEvent(MarketEvent{
				Type:      LiquidityShift,
				TokenID:   tokenID,
				EventID:   new.EventID.String,
				Timestamp: time.Now(),
				OldValue:  old.Liquidity.Float64,
				NewValue:  new.Liquidity.Float64,
				Metadata: map[string]interface{}{
					"percent_change": liqChange * 100,
					"question":       market.Question,
				},
			})
		}
	}
}

// ===== Feature-driven detectors (richer metadata for your strategies) =====

func (g *Gatherer) detectorLoop() {
	for {
		select {
		case <-g.ctx.Done():
			return
		case f := <-g.featuresCh:
			g.detectMomentum(f)
			g.detectMeanRevertHint(f)
		}
	}
}

func (g *Gatherer) detectMomentum(f FeatureUpdate) {
	if f.AvgVol5m <= 0 || f.Vol1m <= 0 { // <-- require real prints
		return
	}

	// gates: spread + volume burst
	if int(f.SpreadBps) > g.config.Thresholds.MaxSpreadBps {
		return
	}
	avg5 := f.AvgVol5m
	if avg5 <= 0 {
		return
	}
	volx := f.Vol1m / avg5
	if volx < g.config.Thresholds.VolSurgeMin {
		return
	}

	// optional imbalance gate
	if g.config.Thresholds.ImbMin > 0 && math.Abs(f.ImbalanceTop) < g.config.Thresholds.ImbMin {
		return
	}

	// directional confirmation
	if f.Ret1m > 0 && (f.SignedFlow1m <= 0 || f.ImbalanceTop <= 0) {
		return
	}
	if f.Ret1m < 0 && (f.SignedFlow1m >= 0 || f.ImbalanceTop >= 0) {
		return
	}

	if g.shouldDebounce(PriceJump, f.TokenID, g.debounceWindow()) {
		return
	}

	g.emitEvent(MarketEvent{
		Type:      PriceJump, // keep strategy compatibility
		TokenID:   f.TokenID,
		Timestamp: f.TS,
		Metadata: map[string]interface{}{
			"ret_1m":         f.Ret1m,
			"ret_5m":         f.Ret5m,
			"vol_1m_over_5m": volx,
			"imbalance_top":  f.ImbalanceTop,
			"spread_bps":     f.SpreadBps,
			"zscore_5m":      f.ZScore5m,
			"broke_high_15m": f.BrokeHigh15m,
			"broke_low_15m":  f.BrokeLow15m,
		},
	})
}

func (g *Gatherer) detectMeanRevertHint(f FeatureUpdate) {
	if math.Abs(f.ZScore5m) < g.config.Thresholds.ZMin {
		return
	}
	if int(f.SpreadBps) > g.config.Thresholds.MaxSpreadBps {
		return
	}
	if f.Vol1m <= 0 { // <-- require some trade flow
		return
	}
	if g.shouldDebounce(PriceJump, f.TokenID, g.debounceWindow()) {
		return
	}
	g.emitEvent(MarketEvent{
		Type:      PriceJump, // strategy-compatible
		TokenID:   f.TokenID,
		Timestamp: f.TS,
		Metadata: map[string]interface{}{
			"zscore_5m":  f.ZScore5m,
			"spread_bps": f.SpreadBps,
			"ret_1m":     f.Ret1m,
			"vol_1m":     f.Vol1m,
			"avg_vol_5m": f.AvgVol5m,
		},
	})
}

func (g *Gatherer) emitEvent(event MarketEvent) {
	g.eventsEmitted++

	// 1) Persist to DB
	meta, _ := json.Marshal(event.Metadata)
	_, err := g.store.RecordMarketEvent(g.ctx, RecordMarketEventParams{
		TokenID:   event.TokenID,
		EventType: sql.NullString{String: string(event.Type), Valid: true},
		OldValue:  sql.NullFloat64{Float64: event.OldValue, Valid: true},
		NewValue:  sql.NullFloat64{Float64: event.NewValue, Valid: true},
		Metadata:  pqtype.NullRawMessage{RawMessage: meta, Valid: true},
	})
	if err != nil {
		g.logger.Error("record event failed", "type", event.Type, "token_id", event.TokenID, "err", err)
	}

	// 2) Non-blocking publish
	select {
	case g.eventChan <- event:
	default:
		g.logger.Warn("event publish queue full; skipping publish",
			"type", event.Type, "token_id", event.TokenID)
	}
}

// shouldDebounce returns true if the same (type, token) fired within 'dur'
func (g *Gatherer) shouldDebounce(t MarketEventType, tokenID string, window time.Duration) bool {
	key := string(t) + ":" + tokenID
	now := time.Now()
	g.emitMu.Lock()
	defer g.emitMu.Unlock()
	if last, ok := g.lastEmit[key]; ok && now.Sub(last) < window {
		return true
	}
	g.lastEmit[key] = now
	return false
}
