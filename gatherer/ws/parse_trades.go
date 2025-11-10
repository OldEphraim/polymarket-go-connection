package ws

import (
	"encoding/json"
	"strings"
	"time"
)

type tradeMsg struct {
	// ids
	TokenID string `json:"token_id"`
	AssetID string `json:"asset_id"`
	// price (aliases)
	Price json.Number `json:"price"`
	P     json.Number `json:"p"`
	// size (aliases)
	Size     json.Number `json:"size"`
	Quantity json.Number `json:"quantity"`
	Q        json.Number `json:"q"`
	// side (aliases)
	Side      string `json:"side"`
	TakerSide string `json:"taker_side"`
	Aggressor string `json:"aggressor"`
	// timestamps (ms or s)
	Timestamp   json.Number `json:"timestamp"`
	TimestampMS json.Number `json:"timestamp_ms"`
	TS          json.Number `json:"ts"`
}

type tradeOut struct {
	assetID string
	price   float64
	size    float64
	side    string // "buy"/"sell"
	ts      time.Time
}

func extractTrades(raw json.RawMessage) []tradeOut {
	// fast path: array
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		out := make([]tradeOut, 0, len(arr))
		for _, el := range arr {
			if t, ok := decodeTrade(el); ok {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	// single object or wrapped
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		// direct
		if t, ok := decodeTrade(raw); ok {
			return []tradeOut{t}
		}
		// nested "trade"
		if v, ok := obj["trade"]; ok {
			if t, ok := decodeTrade(v); ok {
				return []tradeOut{t}
			}
		}
		// nested "trades"
		if v, ok := obj["trades"]; ok {
			var xs []json.RawMessage
			if json.Unmarshal(v, &xs) == nil {
				out := make([]tradeOut, 0, len(xs))
				for _, el := range xs {
					if t, ok := decodeTrade(el); ok {
						out = append(out, t)
					}
				}
				if len(out) > 0 {
					return out
				}
			}
		}
		// nested arrays under "data"/"payload"
		for _, k := range []string{"data", "payload"} {
			if v, ok := obj[k]; ok {
				var xs []json.RawMessage
				if json.Unmarshal(v, &xs) == nil {
					out := make([]tradeOut, 0, len(xs))
					for _, el := range xs {
						if t, ok := decodeTrade(el); ok {
							out = append(out, t)
						}
					}
					if len(out) > 0 {
						return out
					}
				}
			}
		}
	}
	return nil
}

func decodeTrade(b []byte) (tradeOut, bool) {
	var m tradeMsg
	if err := json.Unmarshal(b, &m); err != nil {
		return tradeOut{}, false
	}
	asset := m.TokenID
	if asset == "" {
		asset = m.AssetID
	}
	price := firstFloat(m.Price, m.P)
	size := firstFloat(m.Size, m.Quantity, m.Q)
	side := normSide(firstString(m.Side, m.TakerSide, m.Aggressor))
	ts := pickTime(m)

	if asset == "" || price <= 0 || size <= 0 || side == "" {
		return tradeOut{}, false
	}
	return tradeOut{
		assetID: asset,
		price:   price,
		size:    size,
		side:    side,
		ts:      ts,
	}, true
}

func firstFloat(nums ...json.Number) float64 {
	for _, n := range nums {
		if s := string(n); s != "" {
			if f, err := n.Float64(); err == nil && f > 0 {
				return f
			}
		}
	}
	return 0
}
func firstString(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
func normSide(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "buy", "b", "bid":
		return "buy"
	case "sell", "s", "ask":
		return "sell"
	default:
		return ""
	}
}
func pickTime(m tradeMsg) time.Time {
	// prefer ms
	if s := string(m.TimestampMS); s != "" {
		if ms, err := m.TimestampMS.Int64(); err == nil && ms > 0 {
			return time.Unix(0, ms*int64(time.Millisecond))
		}
	}
	if s := string(m.Timestamp); s != "" {
		if v, err := m.Timestamp.Int64(); err == nil && v > 0 {
			// heuristics: if it looks like ms
			if v > 1_000_000_000_000 {
				return time.Unix(0, v*int64(time.Millisecond))
			}
			return time.Unix(v, 0)
		}
	}
	if s := string(m.TS); s != "" {
		if v, err := m.TS.Int64(); err == nil && v > 0 {
			if v > 1_000_000_000_000 {
				return time.Unix(0, v*int64(time.Millisecond))
			}
			return time.Unix(v, 0)
		}
	}
	return time.Time{}
}
