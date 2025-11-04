package gatherer

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	_ "net/http/pprof" // optional pprof endpoints

	ws "github.com/OldEphraim/polymarket-go-connection/gatherer/ws"
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

	// Channels (ingest → feature engine)
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

	// COPY batcher
	p *Persister

	// internal debug HTTP
	debugSrv *http.Server
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

	// Start internal debug HTTP (opt-in)
	if addr := os.Getenv("GATHERER_DEBUG_ADDR"); addr != "" {
		g.startDebugHTTP(addr)
	}

	// Warm cache
	if err := g.loadMarketCache(); err != nil {
		g.logger.Error("load market cache", "err", err)
	}

	// Initialize COPY persister (uses db.Store.DB())
	// We lazy-cast to the minimal interface the persister expects.
	type rawDB interface{ DB() *sql.DB }
	if rdb, ok := any(g.store).(rawDB); ok {
		g.p = NewPersister(rdb, defaultBatchSize, defaultFlushInterval)
		g.p.Start()
		g.logger.Info("persister started",
			"batch_size", defaultBatchSize,
			"flush_interval_ms", int(defaultFlushInterval/time.Millisecond))
	} else {
		// Persister is required so the feature engine can be the sole consumer
		// of quotes/trades without risking data loss.
		return errors.New("gatherer: store does not expose DB(); persister required")
	}

	// Feature engine
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		runFeatureEngine(g.ctx, g.logger, g.config, g.p, g.featuresCh, g.quotesCh, g.tradesCh)
	}()

	// start channel writers to persist quotes/trades/features
	g.startWriters()

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

func (g *Gatherer) EventChannel() <-chan MarketEvent { return g.eventChan }

// ===== Shared helpers =====

func (g *Gatherer) runWSIngest() {
	backoff := time.Second
	var qCount, tCount int64
	lastReport := time.Now()

	for {
		if g.ctx.Err() != nil {
			return
		}

		assets := g.wsAssetList()
		if len(assets) == 0 {
			// No assets yet — first REST scan hasn’t filled the clob map.
			time.Sleep(2 * time.Second)
			continue
		}

		g.logger.Info("ws subscribing", "assets", len(assets))

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
			case g.quotesCh <- Quote{
				TokenID:   tokenID,
				TS:        ts,
				BestBid:   bestBid,
				BestAsk:   bestAsk,
				SpreadBps: spreadBps,
				Mid:       mid,
			}:
				qCount++
			default:
			}

			// lightweight periodic report (once per 10s)
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
			case g.tradesCh <- Trade{
				TokenID:   tokenID,
				TS:        ts,
				Price:     price,
				Size:      size,
				Aggressor: side,
			}:
				tCount++
			default:
			}
			if time.Since(lastReport) > 10*time.Second {
				g.logger.Info("ws traffic", "quotes", qCount, "trades", tCount)
				lastReport = time.Now()
			}
		}

		wsc := ws.NewPolymarketClient(g.logger)
		err := wsc.Run(g.ctx, g.config.WebsocketURL, assets, onQuote, onTrade)
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

// startWriters drains quotes/trades/features to storage.
func (g *Gatherer) startWriters() {
	// NOTE: Quotes/Trades consumers removed.
	// The feature engine is the single consumer of g.quotesCh/g.tradesCh and
	// is responsible for persistence via the persister to avoid fan-out loss.

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			select {
			case <-g.ctx.Done():
				return
			case f := <-g.featuresCh:
				// If you later add COPY support for features, mirror the pattern.
				if err := g.store.UpsertFeatures(g.ctx, f); err != nil {
					g.logger.Error("upsert features", "token_id", f.TokenID, "err", err)
				}
			}
		}
	}()
}
