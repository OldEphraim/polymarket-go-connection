package gatherer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	"github.com/lib/pq"
)

// Batching knobs (tune if needed)
const (
	defaultBatchSize     = 5000
	defaultFlushInterval = 500 * time.Millisecond
	defaultCapPerType    = 20000 // bounded in-process queues
)

// Persister batches quotes, trades, and features and flushes them with COPY.
// Features go into a staging table, then we call merge_market_features_stage().
type Persister struct {
	db  *sql.DB
	q   *database.Queries
	log *slog.Logger

	batchSize     int
	flushInterval time.Duration

	// Internal batches (protected by mu)
	mu       sync.Mutex
	quotes   []Quote
	trades   []Trade
	features []FeatureUpdate

	// Bounded ingestion channels (non-blocking send/drop)
	quotesCh   chan Quote
	tradesCh   chan Trade
	featuresCh chan FeatureUpdate
	capPerType int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// prevent overlapping flushes
	flushing int32
}

func NewPersister(db *sql.DB, q *database.Queries, log *slog.Logger, batchSize int, flushEvery time.Duration) *Persister {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if flushEvery <= 0 {
		flushEvery = defaultFlushInterval
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Persister{
		db:            db,
		q:             q,
		log:           log,
		batchSize:     batchSize,
		flushInterval: flushEvery,
		quotesCh:      make(chan Quote, defaultCapPerType),
		tradesCh:      make(chan Trade, defaultCapPerType),
		featuresCh:    make(chan FeatureUpdate, defaultCapPerType),
		capPerType:    defaultCapPerType,
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (p *Persister) Start() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		t := time.NewTicker(p.flushInterval)
		defer t.Stop()
		for {
			select {
			case <-p.ctx.Done():
				// Final drain with a fresh timeout context (avoid BeginTx with a canceled ctx)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				p.tryFlush(ctx)
				cancel()
				return
			case <-t.C:
				p.tryFlush(p.ctx)
			case q := <-p.quotesCh:
				p.mu.Lock()
				p.quotes = append(p.quotes, q)
				need := len(p.quotes) >= p.batchSize
				p.mu.Unlock()
				if need {
					p.tryFlush(p.ctx)
				}
			case t := <-p.tradesCh:
				p.mu.Lock()
				p.trades = append(p.trades, t)
				need := len(p.trades) >= p.batchSize
				p.mu.Unlock()
				if need {
					p.tryFlush(p.ctx)
				}
			case f := <-p.featuresCh:
				p.mu.Lock()
				p.features = append(p.features, f)
				need := len(p.features) >= p.batchSize
				p.mu.Unlock()
				if need {
					p.tryFlush(p.ctx)
				}
			}
		}
	}()
}

func (p *Persister) Stop(ctx context.Context) error {
	p.cancel()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Expose a manual flush for tests or ad-hoc drains.
func (p *Persister) Flush(ctx context.Context) error {
	p.tryFlush(ctx)
	// tryFlush swallows the error by design to keep callsites simple.
	// If you want explicit error propagation here, call p.flush(ctx) directly.
	return nil
}

func (p *Persister) tryFlush(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&p.flushing, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&p.flushing, 0)
	if err := p.flush(ctx); err != nil {
		p.log.Error("persister flush error", "err", err)
	}
}

// Enqueue APIs (non-blocking; return false if dropped)
func (p *Persister) EnqueueQuote(q Quote) bool {
	select {
	case p.quotesCh <- q:
		return true
	default:
		return false
	}
}
func (p *Persister) EnqueueTrade(t Trade) bool {
	select {
	case p.tradesCh <- t:
		return true
	default:
		return false
	}
}
func (p *Persister) EnqueueFeatures(f FeatureUpdate) bool {
	select {
	case p.featuresCh <- f:
		return true
	default:
		return false
	}
}

// ---- flush ----

func (p *Persister) flush(ctx context.Context) (err error) {
	// Fast path: nothing to do
	p.mu.Lock()
	if len(p.quotes) == 0 && len(p.trades) == 0 && len(p.features) == 0 {
		p.mu.Unlock()
		return nil
	}
	quotes := p.quotes
	trades := p.trades
	features := p.features
	p.quotes, p.trades, p.features = nil, nil, nil
	p.mu.Unlock()

	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// COPY quotes
	if len(quotes) > 0 {
		stmt, cerr := tx.Prepare(pq.CopyIn(
			"market_quotes",
			"token_id", "ts", "best_bid", "best_ask", "bid_size1", "ask_size1", "spread_bps", "mid",
		))
		if cerr != nil {
			err = cerr
			return err
		}
		for _, q := range quotes {
			if _, cerr = stmt.Exec(
				q.TokenID, q.TS, nullFloat(q.BestBid), nullFloat(q.BestAsk),
				nullFloat(q.BidSize1), nullFloat(q.AskSize1),
				nullFloat(q.SpreadBps), nullFloat(q.Mid),
			); cerr != nil {
				_ = stmt.Close()
				err = cerr
				return err
			}
		}
		if _, cerr = stmt.Exec(); cerr != nil {
			_ = stmt.Close()
			err = cerr
			return err
		}
		if cerr = stmt.Close(); cerr != nil {
			err = cerr
			return err
		}
	}

	// COPY trades
	if len(trades) > 0 {
		stmt, cerr := tx.Prepare(pq.CopyIn(
			"market_trades",
			"token_id", "ts", "price", "size", "aggressor",
		))
		if cerr != nil {
			err = cerr
			return err
		}
		for _, t := range trades {
			if _, cerr = stmt.Exec(
				t.TokenID, t.TS, t.Price, t.Size, t.Aggressor,
			); cerr != nil {
				_ = stmt.Close()
				err = cerr
				return err
			}
		}
		if _, cerr = stmt.Exec(); cerr != nil {
			_ = stmt.Close()
			err = cerr
			return err
		}
		if cerr = stmt.Close(); cerr != nil {
			err = cerr
			return err
		}
	}

	// COPY features → unique TEMP stage, then merge
	if len(features) > 0 {
		tq := p.q.WithTx(tx)

		// Create a unique temp table in THIS session/tx
		tempName, cerr := tq.CreateUniqueTempMarketFeaturesStage(ctx)
		if cerr != nil {
			err = cerr
			return err
		}

		schema, table := splitSchemaTable(tempName) // expect "pg_temp", "mfs_stage_..."
		if schema == "" || table == "" {
			err = fmt.Errorf("unexpected temp table name: %q", tempName)
			return err
		}

		// COPY into the temp table
		stmt, cerr := tx.Prepare(pq.CopyInSchema(
			schema, table,
			"token_id", "ts",
			"ret_1m", "ret_5m",
			"vol_1m", "avg_vol_5m",
			"sigma_5m", "zscore_5m",
			"imbalance_top",
			"spread_bps",
			"broke_high_15m", "broke_low_15m",
			"time_to_resolve_h",
			"signed_flow_1m",
		))
		if cerr != nil {
			err = cerr
			return err
		}
		for _, f := range features {
			if _, cerr = stmt.Exec(
				f.TokenID, f.TS,
				nullFloat(f.Ret1m), nullFloat(f.Ret5m),
				nullFloat(f.Vol1m), nullFloat(f.AvgVol5m),
				nullFloat(f.Sigma5m), nullFloat(f.ZScore5m),
				nullFloat(f.ImbalanceTop),
				nullFloat(f.SpreadBps),
				f.BrokeHigh15m, f.BrokeLow15m,
				nullFloat(f.TimeToResolveH),
				nullFloat(f.SignedFlow1m),
			); cerr != nil {
				_ = stmt.Close()
				err = cerr
				return err
			}
		}
		if _, cerr = stmt.Exec(); cerr != nil {
			_ = stmt.Close()
			err = cerr
			return err
		}
		if cerr = stmt.Close(); cerr != nil {
			err = cerr
			return err
		}

		// Merge (upsert + delete temp rows) via sqlc
		if cerr := tq.MergeMarketFeaturesFrom(ctx, tempName); cerr != nil {
			err = cerr
			return err
		}

	}

	if cerr := tx.Commit(); cerr != nil {
		err = cerr
		return err
	}

	p.log.Debug("persister flush committed",
		"quotes", len(quotes), "trades", len(trades), "features", len(features))
	return nil
}

func nullFloat(f float64) sql.NullFloat64 {
	// treat NaNs as NULL
	if f != f {
		return sql.NullFloat64{Valid: false}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}

func splitSchemaTable(qualified string) (schema, table string) {
	// Expect "pg_temp.name" (returned by our function).
	parts := strings.SplitN(qualified, ".", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1]
	}
	// Fallbacks: if no dot, default to pg_temp
	if !strings.Contains(qualified, ".") && qualified != "" {
		return "pg_temp", qualified
	}
	return "", ""
}
