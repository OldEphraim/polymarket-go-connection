package gatherer

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "net/http/pprof" // optional pprof endpoints

	"github.com/sqlc-dev/pqtype"
)

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
