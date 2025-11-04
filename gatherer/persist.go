package gatherer

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/lib/pq"
)

// Batching knobs (tune if needed)
const (
	defaultBatchSize     = 5000
	defaultFlushInterval = 500 * time.Millisecond
)

// Persister batches quotes, trades, and features and flushes them with COPY.
// Features go into a staging table, then we call merge_market_features_stage().
type Persister struct {
	db *sql.DB

	batchSize     int
	flushInterval time.Duration

	mu       sync.Mutex
	quotes   []Quote
	trades   []Trade
	features []FeatureUpdate

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type rawDB interface {
	DB() *sql.DB
}

func NewPersister(r rawDB, batchSize int, flushEvery time.Duration) *Persister {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if flushEvery <= 0 {
		flushEvery = defaultFlushInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Persister{
		db:            r.DB(),
		batchSize:     batchSize,
		flushInterval: flushEvery,
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
				_ = p.flush()
				return
			case <-t.C:
				_ = p.flush()
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

// Enqueue APIs (thread-safe)

func (p *Persister) EnqueueQuote(q Quote) {
	p.mu.Lock()
	p.quotes = append(p.quotes, q)
	need := len(p.quotes) >= p.batchSize
	p.mu.Unlock()
	if need {
		_ = p.flush()
	}
}

func (p *Persister) EnqueueTrade(t Trade) {
	p.mu.Lock()
	p.trades = append(p.trades, t)
	need := len(p.trades) >= p.batchSize
	p.mu.Unlock()
	if need {
		_ = p.flush()
	}
}

func (p *Persister) EnqueueFeatures(f FeatureUpdate) {
	p.mu.Lock()
	p.features = append(p.features, f)
	need := len(p.features) >= p.batchSize
	p.mu.Unlock()
	if need {
		_ = p.flush()
	}
}

// ---- flush ----

func (p *Persister) flush() error {
	p.mu.Lock()
	if len(p.quotes) == 0 && len(p.trades) == 0 && len(p.features) == 0 {
		p.mu.Unlock()
		return nil
	}
	quotes := p.quotes
	trades := p.trades
	features := p.features
	p.quotes = nil
	p.trades = nil
	p.features = nil
	p.mu.Unlock()

	tx, err := p.db.Begin()
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

	// COPY features → staging, then merge
	if len(features) > 0 {
		stmt, cerr := tx.Prepare(pq.CopyIn(
			"market_features_stage",
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
				// booleans are non-nullable in stage; just pass the bools
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

		// Merge (upsert + truncate stage)
		if _, cerr := tx.Exec(`SELECT merge_market_features_stage()`); cerr != nil {
			err = cerr
			return err
		}
	}

	if cerr := tx.Commit(); cerr != nil {
		err = cerr
		return err
	}
	return nil
}

func nullFloat(f float64) sql.NullFloat64 {
	// treat NaNs as NULL
	if f != f {
		return sql.NullFloat64{Valid: false}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}
