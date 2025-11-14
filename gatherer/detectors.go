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
	if f.AvgVol5m <= 0 || f.Vol1m <= 0 {
		return
	}
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
	if g.config.Thresholds.ImbMin > 0 && math.Abs(f.ImbalanceTop) < g.config.Thresholds.ImbMin {
		return
	}
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
