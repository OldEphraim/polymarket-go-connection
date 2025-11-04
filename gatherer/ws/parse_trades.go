package ws

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type tradeOut struct {
	assetID string
	price   float64
	size    float64
	side    string // "buy"/"sell"
	ts      time.Time
}

func extractTrades(raw json.RawMessage) []tradeOut {
	var out []tradeOut

	// 1) Array of objects
	var arr []map[string]any
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		for _, m := range arr {
			if t, ok := tradeFromAnyMap(m); ok {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// 2) Single object / wrappers
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		// direct
		if t, ok := tradeFromAnyMap(obj); ok {
			out = append(out, t)
		}
		// nested "trade"
		if m, ok := obj["trade"].(map[string]any); ok {
			if t, ok := tradeFromAnyMap(m); ok {
				out = append(out, t)
			}
		}
		// nested "trades"
		if xs, ok := obj["trades"].([]any); ok {
			for _, el := range xs {
				if m, ok := el.(map[string]any); ok {
					if t, ok := tradeFromAnyMap(m); ok {
						out = append(out, t)
					}
				}
			}
		}
		// nested arrays under "data" / "payload"
		if xs, ok := obj["data"].([]any); ok {
			for _, el := range xs {
				if m, ok := el.(map[string]any); ok {
					if t, ok := tradeFromAnyMap(m); ok {
						out = append(out, t)
					}
				}
			}
		}
		if xs, ok := obj["payload"].([]any); ok {
			for _, el := range xs {
				if m, ok := el.(map[string]any); ok {
					if t, ok := tradeFromAnyMap(m); ok {
						out = append(out, t)
					}
				}
			}
		}
	}

	return out
}

func tradeFromAnyMap(m map[string]any) (tradeOut, bool) {
	// asset id
	asset := strAny(m["token_id"])
	if asset == "" {
		asset = strAny(m["asset_id"])
	}
	if asset == "" {
		if mm, ok := m["market"].(map[string]any); ok {
			asset = strAny(mm["token_id"])
			if asset == "" {
				asset = strAny(mm["asset_id"])
			}
		}
	}

	// price
	price := fAny(m["price"])
	if price == 0 {
		price = fAny(m["p"])
	}

	// size / quantity
	size := fAny(m["size"])
	if size == 0 {
		size = fAny(m["quantity"])
	}
	if size == 0 {
		size = fAny(m["q"])
	}

	// side
	side := strAny(m["side"])
	if side == "" {
		side = strAny(m["taker_side"])
	}
	if side == "" {
		side = strAny(m["aggressor"])
	}
	side = normSide(side)

	// timestamp(s)
	ts := time.Time{}
	if v := iAny(m["timestamp"]); v > 0 {
		ts = time.Unix(0, v*int64(time.Millisecond))
	} else if v := iAny(m["timestamp_ms"]); v > 0 {
		ts = time.Unix(0, v*int64(time.Millisecond))
	} else if v := iAny(m["ts"]); v > 0 {
		if v > 1_000_000_000_000 {
			ts = time.Unix(0, v*int64(time.Millisecond))
		} else {
			ts = time.Unix(v, 0)
		}
	}

	t := tradeOut{
		assetID: asset,
		price:   price,
		size:    size,
		side:    side,
		ts:      ts,
	}
	if t.assetID == "" || t.price <= 0 || t.size <= 0 || t.side == "" {
		return tradeOut{}, false
	}
	return t, true
}

func strAny(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	default:
		return ""
	}
}

func fAny(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return f
		}
	}
	return 0
}

func iAny(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case json.Number:
		i, _ := x.Int64()
		return i
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return i
		}
	}
	return 0
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
