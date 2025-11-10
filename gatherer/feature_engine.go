package gatherer

import (
	"container/list"
	"math"
	"time"

	"context"
	"log/slog"
)

// A small rolling window engine (per token)
type featureEngine struct {
	cfg *Config
	log *slog.Logger
	p   *Persister // COPY batcher (quotes/trades/features)

	// input
	quotes <-chan Quote
	trades <-chan Trade

	// output
	out chan<- FeatureUpdate

	// state per token
	state map[string]*rollState
}

type rollState struct {
	lastMid         list.List // store (ts, mid) within max(window)
	last1m          list.List // trades within 1m
	last5m          list.List // trades within 5m
	high15m         float64
	low15m          float64
	lastHighLowScan time.Time

	lastEmit time.Time
}

// Hard caps to prevent unbounded memory growth
const (
	maxMidsPerToken = 512
	maxTrades1m     = 1024
	maxTrades5m     = 4096
)

type priced struct {
	ts  time.Time
	mid float64
}
type traded struct {
	ts     time.Time
	size   float64
	signed float64
} // signed +buy/-sell

func runFeatureEngine(ctx context.Context, log *slog.Logger, cfg *Config, p *Persister,
	out chan<- FeatureUpdate, quotes <-chan Quote, trades <-chan Trade) {

	fe := &featureEngine{
		cfg: cfg, log: log, p: p,
		out: out, quotes: quotes, trades: trades,
		state: map[string]*rollState{},
	}

	cadence := cfg.Stats.FeatCadence
	tick := time.NewTicker(cadence)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case q := <-quotes:
			fe.onQuote(q)
		case t := <-trades:
			fe.onTrade(t)
		case now := <-tick.C:
			fe.emitDue(now)
		}
	}
}

func (fe *featureEngine) st(token string) *rollState {
	if st, ok := fe.state[token]; ok {
		return st
	}
	st := &rollState{}
	fe.state[token] = st
	return st
}

func (fe *featureEngine) onQuote(q Quote) {
	// persist every top-of-book snapshot (COPY-batched)
	if fe.p != nil {
		_ = fe.p.EnqueueQuote(q) // drop if backlog full
	}
	st := fe.st(q.TokenID)
	for st.lastMid.Len() >= maxMidsPerToken {
		st.lastMid.Remove(st.lastMid.Front())
	}
	fe.gcPrices(st, q.TS)

	// update highs/lows every ~15s
	if q.TS.Sub(st.lastHighLowScan) >= 15*time.Second {
		st.lastHighLowScan = q.TS
		hi, lo := fe.windowHighLow(st, 15*time.Minute)
		st.high15m, st.low15m = hi, lo
	}
	// emit opportunistically if cadence elapsed
	fe.maybeEmit(q.TokenID, q.TS, q.SpreadBps, q.Mid)
}

func (fe *featureEngine) onTrade(t Trade) {
	// persist every print (COPY-batched)
	if fe.p != nil {
		_ = fe.p.EnqueueTrade(t) // drop if backlog full
	}
	st := fe.st(t.TokenID)
	sign := 0.0
	if t.Aggressor == "buy" {
		sign = +1
	} else if t.Aggressor == "sell" {
		sign = -1
	}
	st.last1m.PushBack(traded{ts: t.TS, size: t.Size, signed: t.Size * sign})
	st.last5m.PushBack(traded{ts: t.TS, size: t.Size, signed: t.Size * sign})
	for st.last1m.Len() > maxTrades1m {
		st.last1m.Remove(st.last1m.Front())
	}
	for st.last5m.Len() > maxTrades5m {
		st.last5m.Remove(st.last5m.Front())
	}
	fe.gcTrades(st, t.TS)
	fe.maybeEmit(t.TokenID, t.TS, math.NaN(), math.NaN())
}

func (fe *featureEngine) emitDue(now time.Time) {
	for token := range fe.state {
		fe.maybeEmit(token, now, math.NaN(), math.NaN())
	}
}

func (fe *featureEngine) maybeEmit(token string, ts time.Time, spreadBps, mid float64) {
	st := fe.st(token)
	if ts.Sub(st.lastEmit) < fe.cfg.Stats.FeatCadence {
		return
	}

	// compute features
	midNow := mid
	if math.IsNaN(midNow) {
		midNow = fe.latestMid(st)
	}
	ret1m := fe.windowRet(st, fe.cfg.Stats.Ret1m)
	ret5m := fe.windowRet(st, fe.cfg.Stats.Ret5m)

	w1 := fe.cfg.Stats.Vol1m
	if w1 <= 0 {
		w1 = time.Minute
	}
	w5 := fe.cfg.Stats.Vol5m
	if w5 <= 0 {
		w5 = 5 * time.Minute
	}
	sigW := fe.cfg.Stats.Sigma5m
	if sigW <= 0 {
		sigW = 5 * time.Minute
	}

	vol1m, sflow1m := fe.windowVol(st, w1)
	avgVol5m, _ := fe.windowVol(st, w5)

	// compute sigma over returns, not level diffs
	sigma5m := fe.windowSigmaRet(st, sigW)

	floor := fe.cfg.Thresholds.SigmaFloor
	if floor <= 0 {
		floor = 0.01 // 1¢ default
	}
	denom := sigma5m
	if denom < floor {
		denom = floor
	}

	z5 := (midNow - 0.5) / denom
	// clamp to keep hints sane
	if z5 > 10 {
		z5 = 10
	} else if z5 < -10 {
		z5 = -10
	}

	if math.IsNaN(spreadBps) {
		// estimate if needed (not great without best bid/ask)
		spreadBps = 0
	}

	hi, lo := st.high15m, st.low15m
	brokeHi := hi > 0 && midNow >= hi
	brokeLo := lo > 0 && midNow <= lo

	// Skip dull emits: nothing changed and no flow
	if vol1m == 0 && math.Abs(ret1m) < 0.001 && !brokeHi && !brokeLo {
		return
	}

	fu := FeatureUpdate{
		TokenID: token, TS: ts,
		Ret1m: ret1m, Ret5m: ret5m,
		Vol1m: vol1m, AvgVol5m: avgVol5m,
		Sigma5m: sigma5m, ZScore5m: z5,
		SpreadBps: spreadBps, SignedFlow1m: sflow1m,
		BrokeHigh15m: brokeHi, BrokeLow15m: brokeLo,
		// TimeToResolveH: TODO(poly) — compute if you have resolve timestamp
	}

	st.lastEmit = ts

	// persist features (COPY-batched via stage+upsert)
	if fe.p != nil {
		_ = fe.p.EnqueueFeatures(fu) // drop if backlog full
	}

	select {
	case fe.out <- fu:
	default:
	}
}
