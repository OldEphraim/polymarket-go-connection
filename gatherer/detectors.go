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
	// Need at least *some* recent volume
	if f.Vol1m <= 0 {
		return
	}

	// Spread sanity check
	if int(f.SpreadBps) > g.config.Thresholds.MaxSpreadBps {
		return
	}

	// Require at least a 1% 1m move
	if math.Abs(f.Ret1m) < 0.01 {
		return
	}

	// Soft volume surge: if we *have* a 5m baseline, ask for a mild pickup
	volx := 1.0
	if f.AvgVol5m > 0 {
		volx = f.Vol1m / f.AvgVol5m
		// Much looser than the old 2.0x requirement
		if volx < 1.2 {
			return
		}
	}

	// For now, do NOT gate on ImbalanceTop at all — it's basically unused.
	// Also, drop SignedFlow sign-consistency for now to avoid silently killing events
	// while we're just trying to get momentum alive.

	if g.shouldDebounce(PriceJump, f.TokenID, g.debounceWindow()) {
		return
	}

	g.emitEvent(MarketEvent{
		Type:      PriceJump,
		TokenID:   f.TokenID,
		Timestamp: f.TS,

		OldValue: f.Mid1mAgo,
		NewValue: f.MidNow,
		HasOld:   f.Mid1mAgo > 0,
		HasNew:   f.MidNow > 0,

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
	if f.Vol1m <= 0 {
		return
	}

	// Debounce separately from true price jumps
	if g.shouldDebounce(StateExtreme, f.TokenID, g.debounceWindow()) {
		return
	}

	// Compute vol_1m_over_5m so momentum can use it too
	volx := 0.0
	if f.AvgVol5m > 0 {
		volx = f.Vol1m / f.AvgVol5m
	}

	g.emitEvent(MarketEvent{
		Type:      StateExtreme,
		TokenID:   f.TokenID,
		Timestamp: f.TS,

		OldValue: f.Mid1mAgo,
		NewValue: f.MidNow,
		HasOld:   f.Mid1mAgo > 0,
		HasNew:   f.MidNow > 0,

		Metadata: map[string]interface{}{
			"zscore_5m":      f.ZScore5m,
			"spread_bps":     f.SpreadBps,
			"ret_1m":         f.Ret1m,
			"vol_1m":         f.Vol1m,
			"avg_vol_5m":     f.AvgVol5m,
			"vol_1m_over_5m": volx, // <-- new: momentum can reuse this
		},
	})
}

func (g *Gatherer) emitEvent(event MarketEvent) {
	g.eventsEmitted++

	meta, _ := json.Marshal(event.Metadata)

	var oldNull, newNull sql.NullFloat64
	if event.HasOld {
		oldNull = sql.NullFloat64{Float64: event.OldValue, Valid: true}
	}
	if event.HasNew {
		newNull = sql.NullFloat64{Float64: event.NewValue, Valid: true}
	}

	_, err := g.store.RecordMarketEvent(g.ctx, RecordMarketEventParams{
		TokenID:   event.TokenID,
		EventType: sql.NullString{String: string(event.Type), Valid: true},
		OldValue:  oldNull,
		NewValue:  newNull,
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

	if g.lastEmit == nil {
		g.lastEmit = make(map[string]time.Time, 4096)
	}

	if last, ok := g.lastEmit[key]; ok && now.Sub(last) < window {
		return true
	}
	g.lastEmit[key] = now
	return false
}
