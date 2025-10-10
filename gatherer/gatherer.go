package gatherer

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"database/sql"

	"github.com/sqlc-dev/pqtype"
)

type Gatherer struct {
	store    Store
	client   *http.Client
	config   *Config
	logger   *slog.Logger
	lastEmit map[string]time.Time
	emitMu   sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Channels
	eventChan  chan MarketEvent
	quotesCh   chan Quote
	tradesCh   chan Trade
	featuresCh chan FeatureUpdate

	// Cache
	cacheMu     sync.RWMutex
	marketCache map[string]*MarketScanRow

	// Metrics
	scansPerformed int64
	marketsFound   int64
	eventsEmitted  int64

	assetToToken map[string]string // clob asset_id -> market token_id
	assetMu      sync.RWMutex
}

// NOTE: WSClient removed from signature; the gatherer owns WS in ingest_ws.go
func New(store Store, config *Config, logger *slog.Logger) *Gatherer {
	if config == nil {
		config = DefaultConfig()
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())

	// honor EventQueueSize if present
	evSize := 20000
	if config.EventQueueSize > 0 {
		evSize = config.EventQueueSize
	}

	return &Gatherer{
		store:        store,
		client:       &http.Client{Timeout: 10 * time.Second},
		config:       config,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
		eventChan:    make(chan MarketEvent, evSize),
		quotesCh:     make(chan Quote, 50000),
		tradesCh:     make(chan Trade, 100000),
		featuresCh:   make(chan FeatureUpdate, 20000),
		marketCache:  make(map[string]*MarketScanRow),
		lastEmit:     make(map[string]time.Time),
		assetToToken: make(map[string]string),
	}
}

func (g *Gatherer) Start() error {
	g.logger.Info("starting gatherer",
		"scan_interval", g.config.ScanInterval,
		"use_ws", g.config.UseWebsocket)

	// Warm cache
	if err := g.loadMarketCache(); err != nil {
		g.logger.Error("load market cache", "err", err)
	}

	// Feature engine
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		runFeatureEngine(g.ctx, g.logger, g.config, g.store, g.featuresCh, g.quotesCh, g.tradesCh)
	}()

	// Detector loop
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.detectorLoop()
	}()

	// Metrics loop
	g.wg.Add(1)
	go g.metricsLoop()

	// REST scanner loop
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.scanLoop()
	}()

	// WS ingest loop (optional)
	if g.config.UseWebsocket {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.runWSIngest()
		}()
	}

	return nil
}

func (g *Gatherer) Stop() {
	g.logger.Info("stopping gatherer")
	g.cancel()
	g.wg.Wait()
	close(g.eventChan)
	g.logger.Info("stopped",
		"total_scans", g.scansPerformed,
		"total_markets", g.marketsFound,
		"total_events", g.eventsEmitted)
}

func (g *Gatherer) EventChannel() <-chan MarketEvent { return g.eventChan }

// ===== Shared helpers =====

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

func isNaN(f float64) bool { return f != f }

func (g *Gatherer) loadMarketCache() error {
	rows, err := g.store.GetActiveMarketScans(g.ctx, 10000)
	if err != nil {
		return err
	}
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	for i := range rows {
		r := rows[i]
		g.marketCache[r.TokenID] = &r
	}
	g.logger.Info("cache loaded", "count", len(g.marketCache))
	return nil
}

func (g *Gatherer) updateCache(tokenID string, scan *MarketScanRow) {
	g.cacheMu.Lock()
	g.marketCache[tokenID] = scan
	g.cacheMu.Unlock()
}

func (g *Gatherer) lastScan(tokenID string) (*MarketScanRow, bool) {
	g.cacheMu.RLock()
	defer g.cacheMu.RUnlock()
	r, ok := g.marketCache[tokenID]
	return r, ok
}

// Clean price formatting helper
func f64(v float64) sql.NullFloat64 {
	if v == 0 && (1/v) == math.Inf(1) {
		return sql.NullFloat64{Valid: false}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

func (g *Gatherer) addClobMap(tokenID string, clobIDs string) {
	if clobIDs == "" {
		return
	}

	ids := decodeAssetIDs(clobIDs)
	if len(ids) == 0 {
		return
	}

	g.assetMu.Lock()
	for _, id := range ids {
		if id != "" {
			g.assetToToken[id] = tokenID
		}
	}
	g.assetMu.Unlock()
}

func decodeAssetIDs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	// JSON array? e.g. ["id1","id2"]
	if strings.HasPrefix(s, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, v := range arr {
				v = strings.TrimSpace(v)
				v = strings.Trim(v, `"'`)
				if v != "" {
					out = append(out, v)
				}
			}
			return out
		}
	}

	// Fallback: CSV (strip stray quotes/brackets)
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `[]"'"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (g *Gatherer) wsAssetList() []string {
	g.assetMu.RLock()
	defer g.assetMu.RUnlock()
	out := make([]string, 0, len(g.assetToToken))
	for k := range g.assetToToken {
		out = append(out, k)
	}
	return out
}

func (g *Gatherer) tokenForAsset(assetID string) string {
	g.assetMu.RLock()
	defer g.assetMu.RUnlock()
	return g.assetToToken[assetID]
}

func (g *Gatherer) runWSIngest() {
	backoff := time.Second
	for {
		if g.ctx.Err() != nil {
			return
		}
		assets := g.wsAssetList()
		if len(assets) == 0 {
			time.Sleep(2 * time.Second) // wait for first REST scan to fill assetToToken
			continue
		}

		onQuote := func(assetID string, bestBid, bestAsk float64, ts time.Time) {
			g.assetMu.RLock()
			tokenID := g.tokenForAsset(assetID)
			g.assetMu.RUnlock()
			if tokenID == "" {
				return
			}
			mid := (bestBid + bestAsk) / 2
			spreadBps := 0.0
			if mid > 0 {
				spreadBps = (bestAsk - bestBid) / mid * 10000
			}
			select {
			case g.quotesCh <- Quote{
				TokenID:   tokenID,
				TS:        ts,
				BestBid:   bestBid,
				BestAsk:   bestAsk,
				SpreadBps: spreadBps,
				Mid:       mid,
			}:
			default:
			}
		}

		onTrade := func(assetID string, price float64, side string, size float64, ts time.Time) {
			g.assetMu.RLock()
			tokenID := g.tokenForAsset(assetID)
			g.assetMu.RUnlock()
			if tokenID == "" {
				return
			}
			select {
			case g.tradesCh <- Trade{
				TokenID:   tokenID,
				TS:        ts,
				Price:     price,
				Size:      size,
				Aggressor: side, // "buy"/"sell"
			}:
			default:
			}
		}

		// 👇 create the client here (no g.ws field needed)
		ws := NewPolymarketWSClient(g.logger)
		err := ws.Run(g.ctx, g.config.WebsocketURL, assets, onQuote, onTrade)
		if err != nil && g.ctx.Err() == nil {
			g.logger.Warn("ws ingest ended with error; reconnecting", "err", err)
			time.Sleep(backoff)
			if backoff < 5*time.Second {
				backoff += time.Second
			}
			continue
		}
		return
	}
}
