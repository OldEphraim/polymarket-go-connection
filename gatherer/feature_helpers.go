package gatherer

import (
	"container/list"
	"math"
	"time"
)

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
