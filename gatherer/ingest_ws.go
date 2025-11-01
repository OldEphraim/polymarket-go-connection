package gatherer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// ---- Debug toggles (env) ----
var (
	wsDebug       = os.Getenv("POLY_WS_DEBUG") == "1"
	wsLogUnknown  = os.Getenv("POLY_WS_LOG_UNKNOWN") == "1"
	wsUnknownPath = func() string {
		if p := os.Getenv("POLY_WS_UNKNOWN_PATH"); p != "" {
			return p
		}
		return "ws_unknown.jsonl"
	}()
)

func writeUnknown(kind string, raw []byte) {
	if !wsDebug && !wsLogUnknown {
		return
	}
	f, err := os.OpenFile(wsUnknownPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	entry := map[string]any{
		"ts":   time.Now().Format(time.RFC3339Nano),
		"kind": kind,
		"raw":  json.RawMessage(raw),
	}
	if b, err := json.Marshal(entry); err == nil {
		_, _ = f.Write(append(b, '\n'))
	}
}

// ---- Public WS interface ----

type WSClient interface {
	Run(ctx context.Context, url string, assetIDs []string,
		onQuote func(assetID string, bestBid, bestAsk float64, ts time.Time),
		onTrade func(assetID string, price float64, side string, size float64, ts time.Time),
	) error
}

func NewPolymarketWSClient(log *slog.Logger) WSClient {
	if log == nil {
		log = slog.Default()
	}
	return &polyWS{log: log}
}

// ---- Implementation ----

type polyWS struct {
	log *slog.Logger
}

func (c *polyWS) Run(
	ctx context.Context,
	url string,
	assetIDs []string,
	onQuote func(assetID string, bestBid, bestAsk float64, ts time.Time),
	onTrade func(assetID string, price float64, side string, size float64, ts time.Time),
) error {
	if url == "" {
		return errors.New("ws url is empty")
	}
	dialer := websocket.Dialer{
		Proxy:             http.ProxyFromEnvironment,
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: true,
	}

	if wsDebug {
		first := assetIDs
		if len(first) > 10 {
			first = first[:10]
		}
		c.log.Info("ws connecting",
			"url", url,
			"assets", len(assetIDs),
			"sample", first,
		)
	}

	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		if resp != nil && wsDebug {
			c.log.Warn("ws dial failed",
				"url", url,
				"status", resp.StatusCode,
				"err", err,
				"body", tryReadBody(resp),
			)
		}
		return err
	}
	defer conn.Close()

	// Subscribe to MARKET channel (Polymarket expects "type":"market" + "assets_ids")
	sub := map[string]any{
		"type":       "market",
		"assets_ids": assetIDs,
	}
	if err := conn.WriteJSON(sub); err != nil {
		return err
	}

	// Ping loop
	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				_ = conn.WriteMessage(websocket.TextMessage, []byte("PING"))
			}
		}
	}()

	// Read loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		typ, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		if wsDebug {
			writeUnknown("frame", data)
		}

		// PONGs are plain text
		if typ == websocket.TextMessage && string(data) == "PONG" {
			continue
		}

		var env map[string]json.RawMessage
		if err := json.Unmarshal(data, &env); err != nil {
			if wsLogUnknown {
				writeUnknown("unmarshal_env", data)
			}
			continue
		}

		// Event discriminator can be "type", "event", or "event_type"
		et := readString(env, "type")
		if et == "" {
			et = readString(env, "event")
		}
		if et == "" {
			et = readString(env, "event_type")
		}
		if et == "" {
			if wsLogUnknown {
				writeUnknown("no_event_key", data)
			}
			continue
		}

		switch et {
		case "book":
			raw := firstPresent(env["data"], env["payload"])
			if len(raw) == 0 {
				if wsLogUnknown {
					writeUnknown("book_no_payload", data)
				}
				continue
			}
			var b bookMsg
			if err := json.Unmarshal(raw, &b); err != nil {
				if wsLogUnknown {
					writeUnknown("book_bad_payload", data)
				}
				continue
			}
			asset := strings.TrimSpace(firstNonEmpty(b.TokenID, b.AssetID))
			if asset == "" {
				if wsLogUnknown {
					writeUnknown("book_no_asset", data)
				}
				continue
			}
			bestBid, bestAsk := topOfBook(b.Bids), topOfBook(b.Asks)
			if bestBid > 0 && bestAsk > 0 && onQuote != nil {
				onQuote(asset, bestBid, bestAsk, time.Now())
			}

		case "price_change":
			// Variant A (common now): top-level payload with price_changes array
			var gpc gammaPriceChange
			if err := json.Unmarshal(data, &gpc); err == nil && len(gpc.PriceChanges) > 0 {
				ts := time.Now()
				if ms, err := strconv.ParseInt(strings.TrimSpace(gpc.Timestamp), 10, 64); err == nil && ms > 0 {
					ts = time.Unix(0, ms*int64(time.Millisecond))
				}
				for _, ch := range gpc.PriceChanges {
					bid := f64s(ch.BestBid)
					ask := f64s(ch.BestAsk)
					if onQuote != nil && bid > 0 && ask > 0 {
						onQuote(ch.AssetID, bid, ask, ts)
					}
				}
				break
			}

			// Variant B: nested data/payload with { token_id, price_changes: [{best_bid,best_ask}] }
			raw := firstPresent(env["data"], env["payload"])
			if len(raw) == 0 {
				if wsLogUnknown {
					writeUnknown("price_change_no_payload", data)
				}
				break
			}
			var pc priceChangeMsg
			if err := json.Unmarshal(raw, &pc); err != nil {
				if wsLogUnknown {
					writeUnknown("price_change_bad_payload", data)
				}
				break
			}
			if onQuote != nil {
				for _, ch := range pc.Changes {
					if ch.BestBid > 0 && ch.BestAsk > 0 {
						onQuote(pc.TokenID, ch.BestBid, ch.BestAsk, time.Now())
					}
				}
			}

		// trades can arrive with several labels
		case "last_trade_price", "last_trade", "trade", "trades":
			// payload may be in data/payload OR top-level "trades"
			raw := firstPresent(env["data"], env["payload"])
			if len(raw) == 0 {
				if v, ok := env["trades"]; ok {
					raw = v
				}
			}
			if len(raw) == 0 {
				raw = data
			}

			// Extract a fallback frame timestamp (if present)
			fallbackTS := time.Now()
			if s := readString(env, "timestamp"); s != "" {
				if ms, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && ms > 0 {
					fallbackTS = time.Unix(0, ms*int64(time.Millisecond))
				}
			}

			trades := extractTrades(raw, data)
			if len(trades) == 0 {
				if wsLogUnknown {
					writeUnknown(fmt.Sprintf("trade_unknown_%s", et), data)
				}
				continue
			}

			if onTrade != nil {
				for _, t := range trades {
					ts := t.ts
					if ts.IsZero() {
						ts = fallbackTS
					}
					onTrade(t.assetID, t.price, t.side, t.size, ts)
				}
			}

		default:
			if wsLogUnknown {
				writeUnknown("unknown_event_"+et, data)
			}
		}
	}
}

// ---- helpers / payload structs ----

func tryReadBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := ioReadAllN(resp.Body, 4096)
	return string(b)
}

func ioReadAllN(r io.ReadCloser, n int) ([]byte, error) {
	buf := make([]byte, 0, n)
	tmp := make([]byte, 1024)
	total := 0
	for total < n {
		k, err := r.Read(tmp)
		if k > 0 {
			buf = append(buf, tmp[:k]...)
			total += k
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return buf, nil
		}
	}
	return buf, nil
}

func firstPresent(a, b json.RawMessage) json.RawMessage {
	if len(a) > 0 {
		return a
	}
	return b
}

func readString(m map[string]json.RawMessage, key string) string {
	if raw, ok := m[key]; ok && len(raw) > 0 {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---- book / quotes ----

type bookMsg struct {
	TokenID string               `json:"token_id"`
	AssetID string               `json:"asset_id"`
	Bids    [][2]json.RawMessage `json:"bids"` // [["0.52","100"], ...]
	Asks    [][2]json.RawMessage `json:"asks"`
}

func topOfBook(levels [][2]json.RawMessage) float64 {
	if len(levels) == 0 || len(levels[0]) < 1 {
		return 0
	}
	// Commonly strings; try string → float
	var s string
	if err := json.Unmarshal(levels[0][0], &s); err == nil {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	// Fallback numeric
	var f float64
	_ = json.Unmarshal(levels[0][0], &f)
	return f
}

// ---- price_change (two shapes) ----

type priceChangeMsg struct {
	TokenID string       `json:"token_id"`
	Changes []bestChange `json:"price_changes"`
}
type bestChange struct {
	BestBid float64 `json:"best_bid"`
	BestAsk float64 `json:"best_ask"`
}

type gammaChange struct {
	AssetID string `json:"asset_id"`
	Price   string `json:"price"`
	Size    string `json:"size"`
	Side    string `json:"side"` // "BUY"/"SELL"
	Hash    string `json:"hash"`
	BestBid string `json:"best_bid"`
	BestAsk string `json:"best_ask"`
}

// ---- tolerant trade extraction ----

type tradeOut struct {
	assetID string
	price   float64
	size    float64
	side    string // "buy"/"sell"
	ts      time.Time
}

func extractTrades(raw json.RawMessage, _whole []byte) []tradeOut {
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

func f64s(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
