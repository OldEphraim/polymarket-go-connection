package gatherer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	"github.com/sqlc-dev/pqtype"
)

type Gatherer struct {
	store     *db.Store
	client    *http.Client
	eventChan chan MarketEvent
	config    *Config
	logger    *slog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	// Track what we've seen
	marketCache map[string]*database.MarketScan
	cacheMu     sync.RWMutex

	// Metrics
	scansPerformed int64
	marketsFound   int64
	eventsEmitted  int64
}

type Config struct {
	BaseURL          string        `json:"base_url"`
	ScanInterval     time.Duration `json:"scan_interval"`
	LogLevel         string        `json:"log_level"`
	EmitNewMarkets   bool          `json:"emit_new_markets"`
	EmitPriceJumps   bool          `json:"emit_price_jumps"`
	EmitVolumeSpikes bool          `json:"emit_volume_spikes"`
}

func DefaultConfig() *Config {
	return &Config{
		BaseURL:          "https://gamma-api.polymarket.com",
		ScanInterval:     30 * time.Second,
		LogLevel:         "info",
		EmitNewMarkets:   true,
		EmitPriceJumps:   true,
		EmitVolumeSpikes: true,
	}
}

func New(store *db.Store, config *Config, logger *slog.Logger) *Gatherer {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Gatherer{
		store:       store,
		client:      &http.Client{Timeout: 10 * time.Second},
		eventChan:   make(chan MarketEvent, 1000),
		config:      config,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
		marketCache: make(map[string]*database.MarketScan),
	}
}

func (g *Gatherer) Start() error {
	g.logger.Info("Starting gatherer",
		"scan_interval", g.config.ScanInterval,
		"base_url", g.config.BaseURL)

	// Load existing market data into cache
	if err := g.loadMarketCache(); err != nil {
		g.logger.Error("Failed to load market cache", "error", err)
		// Continue anyway - not fatal
	}

	// Start the main scanning loop
	g.wg.Add(1)
	go g.scanLoop()

	// Start metrics reporter
	g.wg.Add(1)
	go g.metricsLoop()

	return nil
}

func (g *Gatherer) Stop() {
	g.logger.Info("Stopping gatherer")
	g.cancel()
	g.wg.Wait()
	close(g.eventChan)

	g.logger.Info("Gatherer stopped",
		"total_scans", g.scansPerformed,
		"total_markets", g.marketsFound,
		"total_events", g.eventsEmitted)
}

func (g *Gatherer) EventChannel() <-chan MarketEvent {
	return g.eventChan
}

func (g *Gatherer) scanLoop() {
	defer g.wg.Done()

	ticker := time.NewTicker(g.config.ScanInterval)
	defer ticker.Stop()

	// Initial scan immediately
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
	startTime := time.Now()
	g.scansPerformed++

	g.logger.Debug("Starting scan cycle", "scan_number", g.scansPerformed)

	var allEvents []PolymarketEvent
	offset := 0
	limit := 100

	// Fetch all events with pagination
	for {
		events, hasMore, err := g.fetchEventsBatch(offset, limit)
		if err != nil {
			g.logger.Error("Failed to fetch events batch",
				"error", err,
				"offset", offset)
			break
		}

		allEvents = append(allEvents, events...)

		if !hasMore || len(events) < limit {
			break
		}

		offset += limit

		// Be nice to the API
		time.Sleep(100 * time.Millisecond)
	}

	g.logger.Info("Fetched all events",
		"count", len(allEvents),
		"duration", time.Since(startTime))

	// Process all markets
	marketCount := 0
	for _, event := range allEvents {
		for _, market := range event.Markets {
			if !market.Closed {
				marketCount++
				g.marketsFound++
				g.processMarket(market, event.ID)
			}
		}
	}

	g.logger.Info("Scan complete",
		"markets_processed", marketCount,
		"duration", time.Since(startTime))
}

func (g *Gatherer) fetchEventsBatch(offset, limit int) ([]PolymarketEvent, bool, error) {
	url := fmt.Sprintf("%s/events?closed=false&order=id&ascending=false&limit=%d&offset=%d",
		g.config.BaseURL, limit, offset)

	g.logger.Debug("Fetching events batch", "offset", offset, "limit", limit)

	resp, err := g.client.Get(url)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		g.logger.Warn("Rate limited, backing off")
		time.Sleep(5 * time.Second)
		return g.fetchEventsBatch(offset, limit) // Retry
	}

	if resp.StatusCode != 200 {
		return nil, false, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var events []PolymarketEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, false, err
	}

	// Check if there might be more
	hasMore := len(events) == limit

	return events, hasMore, nil
}

func (g *Gatherer) processMarket(market PolymarketMarket, eventID string) {
	// Use market.ID as token_id (they're the same in Polymarket)
	tokenID := market.ID

	// Get cached data
	g.cacheMu.RLock()
	oldScan, exists := g.marketCache[tokenID]
	g.cacheMu.RUnlock()

	// Calculate the current price (average of outcome prices for binary markets)
	currentPrice := g.calculateMarketPrice(market)

	// Parse float values from strings
	volume := market.GetVolume()
	liquidity := market.GetLiquidity()

	// Prepare metadata
	metadata := map[string]interface{}{
		"outcome_prices": market.GetOutcomePrices(),
		"condition_id":   market.ConditionID,
		"question_id":    market.QuestionID,
		"volume_24hr":    market.GetVolume24hr(),
		"best_bid":       market.BestBid,
		"best_ask":       market.BestAsk,
	}
	metadataJSON, _ := json.Marshal(metadata)

	// Store in database
	params := database.UpsertMarketScanParams{
		TokenID:    tokenID,
		EventID:    sql.NullString{String: eventID, Valid: eventID != ""},
		Slug:       sql.NullString{String: market.Slug, Valid: market.Slug != ""},
		Question:   sql.NullString{String: market.Question, Valid: market.Question != ""},
		LastPrice:  sql.NullString{String: strconv.FormatFloat(currentPrice, 'f', -1, 64), Valid: true},
		LastVolume: sql.NullString{String: strconv.FormatFloat(volume, 'f', -1, 64), Valid: true},
		Liquidity:  sql.NullString{String: strconv.FormatFloat(liquidity, 'f', -1, 64), Valid: true},
		Metadata:   pqtype.NullRawMessage{RawMessage: metadataJSON, Valid: true},
	}

	// If we have old data, preserve the 24h ago values
	if exists && oldScan != nil {
		params.Price24hAgo = oldScan.Price24hAgo
		params.Volume24hAgo = oldScan.Volume24hAgo
	}

	newScan, err := g.store.UpsertMarketScan(g.ctx, params)
	if err != nil {
		g.logger.Error("Failed to upsert market scan",
			"token_id", tokenID,
			"error", err)
		return
	}

	// Also update the markets table for compatibility with existing code
	_, err = g.store.UpsertMarket(g.ctx, database.UpsertMarketParams{
		TokenID:  tokenID,
		Slug:     sql.NullString{String: market.Slug, Valid: market.Slug != ""},
		Question: sql.NullString{String: market.Question, Valid: market.Question != ""},
		Outcome:  sql.NullString{String: "YES", Valid: true}, // Default for binary markets
	})
	if err != nil {
		g.logger.Error("Failed to update markets table", "token_id", tokenID, "error", err)
	}

	// Update cache
	g.cacheMu.Lock()
	g.marketCache[tokenID] = &newScan
	g.cacheMu.Unlock()

	// Detect and emit events
	if exists && oldScan != nil {
		g.detectEvents(oldScan, &newScan, market)
	} else if g.config.EmitNewMarkets {
		// New market found
		marketAge := time.Since(market.CreatedAt)
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
				"market_age_hours": marketAge.Hours(),
				"liquidity":        liquidity,
			},
		})
	}
}

func (g *Gatherer) calculateMarketPrice(market PolymarketMarket) float64 {
	// Try to use LastTradePrice first
	if market.LastTradePrice > 0 {
		return market.LastTradePrice
	}

	// Otherwise use the first outcome price
	prices := market.GetOutcomePrices()
	if len(prices) > 0 {
		return prices[0]
	}

	// Fallback to bestBid
	return market.BestBid
}

func (g *Gatherer) detectEvents(old, new *database.MarketScan, market PolymarketMarket) {
	tokenID := market.ID

	// Price jump detection (>5% change)
	if g.config.EmitPriceJumps {
		oldPrice, okOld := parseNullableFloat(old.LastPrice)
		newPrice, okNew := parseNullableFloat(new.LastPrice)
		if okOld && okNew && oldPrice > 0 {
			priceChange := (newPrice - oldPrice) / oldPrice
			if abs(priceChange) > 0.05 {
				g.emitEvent(MarketEvent{
					Type:      PriceJump,
					TokenID:   tokenID,
					EventID:   new.EventID.String,
					Timestamp: time.Now(),
					OldValue:  oldPrice,
					NewValue:  newPrice,
					Metadata: map[string]interface{}{
						"percent_change": priceChange * 100,
						"question":       market.Question,
					},
				})
			}
		}
	}

	// Volume spike detection (>200% of previous)
	if g.config.EmitVolumeSpikes {
		oldVol, okOld := parseNullableFloat(old.LastVolume)
		newVol, okNew := parseNullableFloat(new.LastVolume)
		if okOld && okNew && oldVol > 0 && newVol > oldVol*2 {
			g.emitEvent(MarketEvent{
				Type:      VolumeSpike,
				TokenID:   tokenID,
				EventID:   new.EventID.String,
				Timestamp: time.Now(),
				OldValue:  oldVol,
				NewValue:  newVol,
				Metadata: map[string]interface{}{
					"multiplier": newVol / oldVol,
					"question":   market.Question,
				},
			})
		}
	}

	// Liquidity shift detection (>50% change)
	{
		oldLiq, okOld := parseNullableFloat(old.Liquidity)
		newLiq, okNew := parseNullableFloat(new.Liquidity)
		if okOld && okNew && oldLiq > 0 {
			liqChange := (newLiq - oldLiq) / oldLiq
			if abs(liqChange) > 0.5 {
				g.emitEvent(MarketEvent{
					Type:      LiquidityShift,
					TokenID:   tokenID,
					EventID:   new.EventID.String,
					Timestamp: time.Now(),
					OldValue:  oldLiq,
					NewValue:  newLiq,
					Metadata: map[string]interface{}{
						"percent_change": liqChange * 100,
						"question":       market.Question,
					},
				})
			}
		}
	}
}

func (g *Gatherer) emitEvent(event MarketEvent) {
	g.eventsEmitted++

	select {
	case g.eventChan <- event:
		g.logger.Debug("Emitted event",
			"type", event.Type,
			"token_id", event.TokenID)

		// Also record to database for analysis
		metadata, _ := json.Marshal(event.Metadata)
		_, err := g.store.RecordMarketEvent(g.ctx, database.RecordMarketEventParams{
			TokenID:   event.TokenID,
			EventType: sql.NullString{String: string(event.Type), Valid: true},
			OldValue:  sql.NullString{String: strconv.FormatFloat(event.OldValue, 'f', -1, 64), Valid: true},
			NewValue:  sql.NullString{String: strconv.FormatFloat(event.NewValue, 'f', -1, 64), Valid: true},
			Metadata:  pqtype.NullRawMessage{RawMessage: metadata, Valid: true},
		})
		if err != nil {
			g.logger.Error("Failed to record event", "error", err)
		}
	default:
		g.logger.Warn("Event channel full, dropping event",
			"type", event.Type,
			"token_id", event.TokenID)
	}
}

func (g *Gatherer) loadMarketCache() error {
	// Load recent market scans into memory for comparison
	markets, err := g.store.GetActiveMarketScans(g.ctx, 10000) // Load up to 10k markets
	if err != nil {
		return err
	}

	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()

	for _, market := range markets {
		marketCopy := market // Important: create a copy
		g.marketCache[market.TokenID] = &marketCopy
	}

	g.logger.Info("Loaded market cache", "count", len(g.marketCache))
	return nil
}

func (g *Gatherer) metricsLoop() {
	defer g.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			g.cacheMu.RLock()
			cacheSize := len(g.marketCache)
			g.cacheMu.RUnlock()

			g.logger.Info("Gatherer metrics",
				"scans_performed", g.scansPerformed,
				"markets_found", g.marketsFound,
				"events_emitted", g.eventsEmitted,
				"cache_size", cacheSize)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// parseNullableFloat converts sql.NullString holding a numeric string to float64.
// Returns (value, true) if valid; (0, false) otherwise.
func parseNullableFloat(ns sql.NullString) (float64, bool) {
	if !ns.Valid {
		return 0, false
	}
	v, err := strconv.ParseFloat(ns.String, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
