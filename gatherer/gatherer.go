package gatherer

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	ws "github.com/OldEphraim/polymarket-go-connection/gatherer/ws"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type Gatherer struct {
	store    Store
	db       *sql.DB
	sqlc     *database.Queries
	client   *http.Client
	config   *Config
	logger   *slog.Logger
	lastEmit map[string]time.Time
	emitMu   sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Channels (ingest → feature engine)
	eventChan  chan MarketEvent
	quotesCh   chan Quote
	tradesCh   chan Trade
	featuresCh chan FeatureUpdate

	// Cache
	cacheMu     sync.RWMutex
	marketCache map[string]*cacheEntry

	// Metrics
	scansPerformed int64
	marketsFound   int64
	eventsEmitted  int64

	assetToToken map[string]string // clob asset_id -> market token_id
	assetMu      sync.RWMutex

	// COPY batcher
	p *Persister

	// internal debug HTTP
	debugSrv *http.Server
}

func New(store Store, config *Config, logger *slog.Logger, db *sql.DB, sqlc *database.Queries) *Gatherer {
	if config == nil {
		config = DefaultConfig()
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Right-size queues (still generous, but not OOM-bait)
	evSize := 2000
	if config.EventQueueSize > 0 && config.EventQueueSize < 50_000 {
		evSize = config.EventQueueSize
	}

	// Reuse a single HTTP client with a tuned Transport (fewer TLS/header allocs)
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second}

	g := &Gatherer{
		store:      store,
		db:         db,
		sqlc:       sqlc,
		client:     client,
		config:     config,
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
		eventChan:  make(chan MarketEvent, evSize),
		quotesCh:   make(chan Quote, 5000),
		tradesCh:   make(chan Trade, 10000),
		featuresCh: make(chan FeatureUpdate, 5000),
		// Lazy-init maps on first use to avoid big upfront allocs in pprof “gatherer.New”
		// (we’ll check nil before write and make(...) as needed)
		marketCache:  nil,
		lastEmit:     nil,
		assetToToken: nil,
	}
	return g
}

func (g *Gatherer) Start() error {
	g.logger.Info("starting gatherer",
		"scan_interval", g.config.ScanInterval,
		"use_ws", g.config.UseWebsocket)

	// Start internal debug HTTP (opt-in)
	if addr := os.Getenv("GATHERER_DEBUG_ADDR"); addr != "" {
		g.startDebugHTTP(addr)
	}

	// Warm cache
	if err := g.loadMarketCache(); err != nil {
		g.logger.Error("load market cache", "err", err)
	}

	// Seed WS asset map from DB before anything else
	if err := g.seedAssetsFromDB(); err != nil {
		g.logger.Warn("seed assets from DB failed", "err", err)
	}

	// Initialize COPY persister (explicit deps; no type-asserts)
	g.p = NewPersister(g.db, g.sqlc, g.logger, defaultBatchSize, defaultFlushInterval)
	g.p.Start()
	g.logger.Info("persister started",
		"batch_size", defaultBatchSize,
		"flush_interval_ms", int(defaultFlushInterval/time.Millisecond))

	// Feature engine
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		runFeatureEngine(g.ctx, g.logger, g.config, g.p, g.featuresCh, g.quotesCh, g.tradesCh)
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

	// WS ingest loop
	if g.config.UseWebsocket {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			// wait until we have a healthy asset set
			target := 20000 // tune: minimum assets before first connect
			deadline := time.Now().Add(45 * time.Second)
			for {
				if len(g.wsAssetList()) >= target {
					break
				}
				if time.Now().After(deadline) {
					break
				} // don’t block forever; we’ll resubscribe later
				time.Sleep(1 * time.Second)
			}
			g.runWSIngest()
		}()
	}

	return nil
}

func (g *Gatherer) Stop() {
	g.logger.Info("stopping gatherer")

	// stop debug HTTP first so probes don't keep poking during shutdown
	if g.debugSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = g.debugSrv.Shutdown(ctx)
		cancel()
	}

	g.cancel()
	g.wg.Wait()
	close(g.eventChan)

	// stop persister last so any final feature flush can enqueue safely
	if g.p != nil {
		_ = g.p.Stop(context.Background())
	}

	g.logger.Info("stopped",
		"total_scans", g.scansPerformed,
		"total_markets", g.marketsFound,
		"total_events", g.eventsEmitted)
}

func (g *Gatherer) runWSIngest() {
	backoff := time.Second
	var qCount, tCount int64
	lastReport := time.Now()

	lastSubscribed := 0

	for {
		if g.ctx.Err() != nil {
			return
		}

		assets := g.wsAssetList()
		if len(assets) == 0 {
			time.Sleep(2 * time.Second)
			continue
		}

		// dedupe + snapshot count
		seen := make(map[string]struct{}, len(assets))
		clean := cleanIDs(assets, seen)
		lastSubscribed = len(clean)

		g.logger.Info("ws subscribing", "assets", lastSubscribed)

		// child ctx so we can trigger reconnect when asset set grows
		wsCtx, cancel := context.WithCancel(g.ctx)

		// monitor growth and force reconnect if it increases significantly
		go func(prev int, cancel context.CancelFunc) {
			t := time.NewTicker(2 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-wsCtx.Done():
					return
				case <-t.C:
					cur := len(g.wsAssetList())
					// reconnect if +10% or +1,000 IDs, whichever is larger
					if cur >= prev+1000 || (prev > 0 && float64(cur) >= float64(prev)*1.10) {
						g.logger.Info("ws resubscribe triggered", "prev_assets", prev, "new_assets", cur)
						cancel()
						return
					}
				}
			}
		}(lastSubscribed, cancel)

		onQuote := func(assetID string, bestBid, bestAsk float64, ts time.Time) {
			tokenID := g.tokenForAsset(assetID)
			if tokenID == "" {
				return
			}
			mid := (bestBid + bestAsk) / 2
			spreadBps := 0.0
			if mid > 0 {
				spreadBps = (bestAsk - bestBid) / mid * 10000
			}
			select {
			case g.quotesCh <- Quote{TokenID: tokenID, TS: ts, BestBid: bestBid, BestAsk: bestAsk, SpreadBps: spreadBps, Mid: mid}:
				qCount++
			default:
			}
			if time.Since(lastReport) > 10*time.Second {
				g.logger.Info("ws traffic", "quotes", qCount, "trades", tCount)
				lastReport = time.Now()
			}
		}

		onTrade := func(assetID string, price float64, side string, size float64, ts time.Time) {
			tokenID := g.tokenForAsset(assetID)
			if tokenID == "" {
				return
			}
			select {
			case g.tradesCh <- Trade{TokenID: tokenID, TS: ts, Price: price, Size: size, Aggressor: side}:
				tCount++
			default:
			}
			if time.Since(lastReport) > 10*time.Second {
				g.logger.Info("ws traffic", "quotes", qCount, "trades", tCount)
				lastReport = time.Now()
			}
		}

		wsc := ws.NewPolymarketClient(g.logger)
		err := wsc.Run(wsCtx, g.config.WebsocketURL, clean, onQuote, onTrade)
		cancel() // ensure the monitor goroutine exits

		if err != nil && g.ctx.Err() == nil {
			g.logger.Warn("ws ingest ended; reconnecting", "err", err)
			time.Sleep(backoff)
			if backoff < 5*time.Second {
				backoff += time.Second
			}
			continue
		}
		// Normal exit or parent context cancelled.
		return
	}
}

func cleanIDs(ids []string, seen map[string]struct{}) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
