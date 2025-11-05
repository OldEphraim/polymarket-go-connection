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
		fe.p.EnqueueQuote(q)
	}
	st := fe.st(q.TokenID)
	st.lastMid.PushBack(priced{ts: q.TS, mid: q.Mid})
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
		fe.p.EnqueueTrade(t)
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
		fe.p.EnqueueFeatures(fu)
	}

	select {
	case fe.out <- fu:
	default:
	}
}

// ===== window helpers =====
func (fe *featureEngine) gcPrices(st *rollState, now time.Time) {
	// keep up to the largest price-derived window we might read from
	ret5 := fe.cfg.Stats.Ret5m
	if ret5 <= 0 {
		ret5 = 5 * time.Minute
	}
	sigW := fe.cfg.Stats.Sigma5m
	if sigW <= 0 {
		sigW = 5 * time.Minute
	}
	// keep enough for 15m high/low too
	keep := ret5
	if sigW > keep {
		keep = sigW
	}
	if (15 * time.Minute) > keep {
		keep = 15 * time.Minute
	}

	cut := now.Add(-keep)
	for st.lastMid.Len() > 0 {
		fr := st.lastMid.Front().Value.(priced)
		if fr.ts.Before(cut) {
			st.lastMid.Remove(st.lastMid.Front())
		} else {
			break
		}
	}
}

func (fe *featureEngine) gcTrades(st *rollState, now time.Time) {
	w1 := fe.cfg.Stats.Vol1m
	if w1 <= 0 {
		w1 = time.Minute
	}
	w5 := fe.cfg.Stats.Vol5m
	if w5 <= 0 {
		w5 = 5 * time.Minute
	}

	cut1 := now.Add(-w1)
	for st.last1m.Len() > 0 {
		fr := st.last1m.Front().Value.(traded)
		if fr.ts.Before(cut1) {
			st.last1m.Remove(st.last1m.Front())
		} else {
			break
		}
	}

	cut5 := now.Add(-w5)
	for st.last5m.Len() > 0 {
		fr := st.last5m.Front().Value.(traded)
		if fr.ts.Before(cut5) {
			st.last5m.Remove(st.last5m.Front())
		} else {
			break
		}
	}
}

func (fe *featureEngine) latestMid(st *rollState) float64 {
	if st.lastMid.Len() == 0 {
		return 0
	}
	return st.lastMid.Back().Value.(priced).mid
}

func (fe *featureEngine) windowRet(st *rollState, dur time.Duration) float64 {
	if st.lastMid.Len() == 0 {
		return 0
	}
	now := st.lastMid.Back().Value.(priced)
	cut := now.ts.Add(-dur)
	// find first >= cut
	for e := st.lastMid.Front(); e != nil; e = e.Next() {
		p := e.Value.(priced)
		if !p.ts.Before(cut) && p.mid > 0 {
			if now.mid == 0 {
				return 0
			}
			return (now.mid - p.mid) / p.mid
		}
	}
	// not enough history
	return 0
}

func (fe *featureEngine) windowVol(st *rollState, dur time.Duration) (sum float64, signed float64) {
	var l *list.List
	if dur <= time.Minute {
		l = &st.last1m
	} else {
		l = &st.last5m
	}
	for e := l.Front(); e != nil; e = e.Next() {
		t := e.Value.(traded)
		sum += t.size
		signed += t.signed
	}
	return
}

// stddev of simple returns over the window; require a few samples
func (fe *featureEngine) windowSigmaRet(st *rollState, dur time.Duration) float64 {
	if st.lastMid.Len() < 3 {
		return 0
	}
	now := st.lastMid.Back().Value.(priced)
	cut := now.ts.Add(-dur)

	// collect mids inside window, newest→oldest
	var xs []float64
	for e := st.lastMid.Back(); e != nil; e = e.Prev() {
		p := e.Value.(priced)
		if p.ts.Before(cut) {
			break
		}
		xs = append(xs, p.mid)
	}
	if len(xs) < 3 {
		return 0
	}

	// simple returns between consecutive mids
	rets := make([]float64, 0, len(xs)-1)
	for i := 1; i < len(xs); i++ {
		a, b := xs[i-1], xs[i]
		if a <= 0 || b <= 0 { // guard
			continue
		}
		rets = append(rets, (a-b)/b)
	}
	if len(rets) < 2 {
		return 0
	}

	// stddev
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var s2 float64
	for _, r := range rets {
		d := r - mean
		s2 += d * d
	}
	s2 /= float64(len(rets) - 1)
	return math.Sqrt(s2)
}

func (fe *featureEngine) windowHighLow(st *rollState, dur time.Duration) (hi, lo float64) {
	if st.lastMid.Len() == 0 {
		return 0, 0
	}
	now := st.lastMid.Back().Value.(priced)
	cut := now.ts.Add(-dur)
	hi, lo = -1.0, 2.0
	for e := st.lastMid.Back(); e != nil; e = e.Prev() {
		p := e.Value.(priced)
		if p.ts.Before(cut) {
			break
		}
		if p.mid > hi {
			hi = p.mid
		}
		if p.mid < lo {
			lo = p.mid
		}
	}
	return
}
