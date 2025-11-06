package gatherer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sqlc-dev/pqtype"
)

func (g *Gatherer) scanLoop() {
	defer func() {
		// on exit
	}()

	ticker := time.NewTicker(g.config.ScanInterval)
	defer ticker.Stop()

	// initial scan
	g.performScan()

	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			g.performScan()
		}
	}
}

func (g *Gatherer) performScan() {
	start := time.Now()
	g.scansPerformed++

	var allEvents []PolymarketEvent
	offset := 0
	limit := 100

	for {
		events, hasMore, err := g.fetchEventsBatch(offset, limit)
		if err != nil {
			g.logger.Error("fetch events batch", "offset", offset, "err", err)
			break
		}
		allEvents = append(allEvents, events...)

		if !hasMore || len(events) < limit {
			break
		}
		offset += limit

		// be polite to the API
		time.Sleep(100 * time.Millisecond)
	}

	marketCount := 0
	for _, ev := range allEvents {
		for _, m := range ev.Markets {
			if m.Closed {
				continue
			}
			marketCount++
			g.marketsFound++
			g.processMarket(m, ev.ID)
		}
	}

	g.logger.Info("scan complete",
		"events", len(allEvents),
		"markets_processed", marketCount,
		"took", time.Since(start))
}

func (g *Gatherer) fetchEventsBatch(offset, limit int) ([]PolymarketEvent, bool, error) {
	url := fmt.Sprintf("%s/events?closed=false&order=id&ascending=false&limit=%d&offset=%d",
		g.config.BaseURL, limit, offset)

	resp, err := g.client.Get(url)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		g.logger.Warn("rate limited; sleeping")
		time.Sleep(5 * time.Second)
		return g.fetchEventsBatch(offset, limit)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var events []PolymarketEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, false, err
	}
	return events, len(events) == limit, nil
}

func (g *Gatherer) processMarket(market PolymarketMarket, eventID string) {
	tokenID := market.ID

	// get cached prior
	g.cacheMu.RLock()
	oldScan, exists := g.marketCache[tokenID]
	g.cacheMu.RUnlock()

	// price
	currentPrice := g.calculateMarketPrice(market)

	// numeric volume/liquidity directly from payload
	// (prefer total volume if present; can be swapped to Volume24hr if we want 24h flow)
	// TODO: total volume and Volume24hr are different things, and confusion exists here because we need an explicit way for the gatherer to see when a market has resolved. This does not yet exist.
	volume := market.VolumeNum
	if volume == 0 && market.Volume24hr > 0 {
		volume = market.Volume24hr
	}
	liquidity := market.LiquidityNum

	// metadata to stash for later analysis
	metadata := map[string]interface{}{
		"outcome_prices": market.OutcomePrices, // may be nil/empty, fine
		"condition_id":   market.ConditionID,
		"question_id":    market.QuestionID,
		"volume_24hr":    market.Volume24hr,
		"best_bid":       market.BestBid,
		"best_ask":       market.BestAsk,
		"clob_token_ids": market.ClobTokenIds,
	}
	metaJSON, _ := json.Marshal(metadata)

	// Remember asset-id -> token-id mapping for WS
	g.addClobMap(tokenID, market.ClobTokenIds)

	// upsert scan snapshot
	params := UpsertMarketScanParams{
		TokenID:    tokenID,
		EventID:    sql.NullString{String: eventID, Valid: eventID != ""},
		Slug:       sql.NullString{String: market.Slug, Valid: market.Slug != ""},
		Question:   sql.NullString{String: market.Question, Valid: market.Question != ""},
		LastPrice:  sql.NullFloat64{Float64: currentPrice, Valid: true},
		LastVolume: sql.NullFloat64{Float64: volume, Valid: true},
		Liquidity:  sql.NullFloat64{Float64: liquidity, Valid: true},
		Metadata:   pqtype.NullRawMessage{RawMessage: metaJSON, Valid: true},
	}
	if exists && oldScan != nil {
		params.Price24hAgo = oldScan.Price24hAgo
		params.Volume24hAgo = oldScan.Volume24hAgo
	}

	newScan, err := g.store.UpsertMarketScan(g.ctx, params)
	if err != nil {
		g.logger.Error("upsert market_scan", "token", tokenID, "err", err)
		return
	}

	// keep markets table in sync (compat with existing code)
	_, _ = g.store.UpsertMarket(g.ctx, UpsertMarketParams{
		TokenID:  tokenID,
		Slug:     sql.NullString{String: market.Slug, Valid: market.Slug != ""},
		Question: sql.NullString{String: market.Question, Valid: market.Question != ""},
		Outcome:  sql.NullString{String: "YES", Valid: true}, // default for binaries
	})

	// update cache
	g.cacheMu.Lock()
	g.marketCache[tokenID] = &newScan
	g.cacheMu.Unlock()

	// Only push a quote when both sides are present
	bestBid, bestAsk := market.BestBid, market.BestAsk
	if bestBid > 0 && bestAsk > 0 { // <-- gate
		mid := (bestBid + bestAsk) / 2
		spreadBps := 0.0
		if mid > 0 {
			spreadBps = (bestAsk - bestBid) / mid * 10000
		}
		select {
		case g.quotesCh <- Quote{
			TokenID:   tokenID,
			TS:        time.Now(),
			BestBid:   bestBid,
			BestAsk:   bestAsk,
			SpreadBps: spreadBps,
			Mid:       mid,
		}:
		default:
		}
	}

	// detectors
	if exists && oldScan != nil {
		// keep existing detector (legacy) that compares old/new scans
		g.detectLegacy(&newScan, oldScan, market)
	} else if g.config.EmitNewMarkets {
		ageHours := 0.0
		// CreatedAt might be zero if missing; guard it
		if market.CreatedAt != nil && !market.CreatedAt.IsZero() {
			ageHours = time.Since(*market.CreatedAt).Hours()
		}
		g.emitEvent(MarketEvent{
			Type:      NewMarket,
			TokenID:   tokenID,
			EventID:   eventID,
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"question":         market.Question,
				"volume":           volume,
				"slug":             market.Slug,
				"created_at":       market.CreatedAt,
				"market_age_hours": ageHours,
				"liquidity":        liquidity,
			},
		})
	}
}

func (g *Gatherer) calculateMarketPrice(m PolymarketMarket) float64 {
	// Prefer last trade; else mid; else bid
	if m.LastTradePrice > 0 {
		return m.LastTradePrice
	}
	if m.BestBid > 0 && m.BestAsk > 0 {
		return (m.BestBid + m.BestAsk) / 2
	}
	return m.BestBid
}
