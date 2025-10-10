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

	// ---- log what we’re subscribing to
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
		// Helpful body on 404 etc.
		if resp != nil && wsDebug {
			buf := make([]byte, 2048)
			n, _ := resp.Body.Read(buf)
			c.log.Warn("ws dial failed",
				"url", url,
				"status", resp.StatusCode,
				"err", err,
				"body", string(buf[:n]),
			)
		}
		return err
	}
	defer conn.Close()

	// Subscribe to MARKET channel
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

		// optional: dump every frame
		if wsDebug {
			writeUnknown("frame", data)
		}

		// Handle PONG (text)
		if typ == websocket.TextMessage && string(data) == "PONG" {
			continue
		}

		var env map[string]json.RawMessage
		if err := json.Unmarshal(data, &env); err != nil {
			// dump unparseable
			if wsLogUnknown {
				writeUnknown("unmarshal_env", data)
			}
			continue
		}

		// find the event type (may be "type", "event", or "event_type")
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
			asset := strings.TrimSpace(b.TokenID)
			if asset == "" {
				asset = strings.TrimSpace(b.AssetID)
			}
			if asset == "" {
				if wsLogUnknown {
					writeUnknown("book_no_asset", data)
				}
				continue
			}
			bestBid, bestAsk := topOfBook(b.Bids), topOfBook(b.Asks)
			// emit only when both sides exist
			if bestBid > 0 && bestAsk > 0 && onQuote != nil {
				ts := time.Now()
				onQuote(asset, bestBid, bestAsk, ts)
			}

		case "price_change":
			// Polymarket Gamma sends price_change as a TOP-LEVEL object
			var gpc gammaPriceChange
			if err := json.Unmarshal(data, &gpc); err == nil && len(gpc.PriceChanges) > 0 {
				// parse timestamp (ms in a string)
				ts := time.Now()
				if ms, err := strconv.ParseInt(gpc.Timestamp, 10, 64); err == nil && ms > 0 {
					ts = time.Unix(0, ms*int64(time.Millisecond))
				}

				for _, ch := range gpc.PriceChanges {
					bid := f64s(ch.BestBid)
					ask := f64s(ch.BestAsk)

					// 1) QUOTE: only when both sides exist
					if onQuote != nil && bid > 0 && ask > 0 {
						onQuote(ch.AssetID, bid, ask, ts)
					}

					// 2) TRADE: when size>0 and price>0
					price := f64s(ch.Price)
					size := f64s(ch.Size)
					if onTrade != nil && price > 0 && size > 0 {
						onTrade(ch.AssetID, price, normSide(strings.ToLower(ch.Side)), size, ts)
					}
				}
				break
			}

			// Fallback to your older nested shape, if Polymarket ever sends it
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

		// ---- NEW: accept multiple trade variants
		case "last_trade_price", "last_trade", "trade", "trades":
			raw := firstPresent(env["data"], env["payload"])
			if len(raw) == 0 {
				if wsLogUnknown {
					writeUnknown("trade_no_payload", data)
				}
				continue
			}

			// Use tolerant extractor (handles many shapes)
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
						ts = time.Now()
					}
					onTrade(t.assetID, t.price, t.side, t.size, ts)
				}
			}

			// unknown trade shape → write it
			if wsLogUnknown {
				writeUnknown(fmt.Sprintf("trade_unknown_%s", et), data)
			}

		default:
			// only stash unknowns if asked
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
			if errors.Is(err, os.ErrClosed) {
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

func trim(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(" + strconv.Itoa(len(b)) + " bytes)"
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
	TokenID string               `json:"token_id"` // also seen as asset_id
	AssetID string               `json:"asset_id"`
	Bids    [][2]json.RawMessage `json:"bids"` // [["0.52","100"], ...] as strings
	Asks    [][2]json.RawMessage `json:"asks"`
}

func topOfBook(levels [][2]json.RawMessage) float64 {
	if len(levels) == 0 || len(levels[0]) < 1 {
		return 0
	}
	// Polymarket often sends strings; be permissive
	var s string
	if err := json.Unmarshal(levels[0][0], &s); err == nil {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	// Try numeric directly
	var f float64
	_ = json.Unmarshal(levels[0][0], &f)
	return f
}

// ---- price_change (best bid/ask deltas) ----

type priceChangeMsg struct {
	TokenID string       `json:"token_id"`
	Changes []bestChange `json:"price_changes"`
}
type bestChange struct {
	BestBid float64 `json:"best_bid"`
	BestAsk float64 `json:"best_ask"`
}

// ---- tolerant trade extraction ----

type tradeOut struct {
	assetID string
	price   float64
	size    float64
	side    string // "buy"/"sell"
	ts      time.Time
}

func extractTrades(raw json.RawMessage, whole []byte) []tradeOut {
	var out []tradeOut

	// 1) If it's an array, try each element as a trade-ish map
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

	// 2) Object wrapper: may have "trade" or "trades" or be the trade itself
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		// a) direct
		if t, ok := tradeFromAnyMap(obj); ok {
			out = append(out, t)
		}
		// b) nested "trade"
		if m, ok := obj["trade"].(map[string]any); ok {
			if t, ok := tradeFromAnyMap(m); ok {
				out = append(out, t)
			}
		}
		// c) nested "trades"
		if xs, ok := obj["trades"].([]any); ok {
			for _, el := range xs {
				if m, ok := el.(map[string]any); ok {
					if t, ok := tradeFromAnyMap(m); ok {
						out = append(out, t)
					}
				}
			}
		}
		// d) nested "data"/"payload" as arrays
		if m, ok := obj["data"].(map[string]any); ok {
			if t, ok := tradeFromAnyMap(m); ok {
				out = append(out, t)
			}
		}
		if xs, ok := obj["data"].([]any); ok {
			for _, el := range xs {
				if m, ok := el.(map[string]any); ok {
					if t, ok := tradeFromAnyMap(m); ok {
						out = append(out, t)
					}
				}
			}
		}
		if m, ok := obj["payload"].(map[string]any); ok {
			if t, ok := tradeFromAnyMap(m); ok {
				out = append(out, t)
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
	// Try common IDs
	asset := strAny(m["token_id"])
	if asset == "" {
		asset = strAny(m["asset_id"])
	}
	if asset == "" {
		// sometimes nested market
		if mm, ok := m["market"].(map[string]any); ok {
			asset = strAny(mm["token_id"])
			if asset == "" {
				asset = strAny(mm["asset_id"])
			}
		}
	}

	// Price: "price" or short "p"
	price := fAny(m["price"])
	if price == 0 {
		price = fAny(m["p"])
	}

	// Size/qty: "size", "quantity", short "q"
	size := fAny(m["size"])
	if size == 0 {
		size = fAny(m["quantity"])
	}
	if size == 0 {
		size = fAny(m["q"])
	}

	// Side/aggressor: "side", "taker_side", "aggressor", also normalize ask/bid
	side := strAny(m["side"])
	if side == "" {
		side = strAny(m["taker_side"])
	}
	if side == "" {
		side = strAny(m["aggressor"])
	}
	side = normSide(side)

	// Timestamp: millis "timestamp" or "timestamp_ms" or "ts"
	ts := time.Time{}
	if v := iAny(m["timestamp"]); v > 0 {
		ts = time.Unix(0, v*int64(time.Millisecond))
	} else if v := iAny(m["timestamp_ms"]); v > 0 {
		ts = time.Unix(0, v*int64(time.Millisecond))
	} else if v := iAny(m["ts"]); v > 0 {
		// some feeds use seconds
		if v > 1_000_000_000_000 { // looks like ms
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
	// minimally require asset, price, size, side
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
		if f, err := strconv.ParseFloat(x, 64); err == nil {
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
		if i, err := strconv.ParseInt(x, 10, 64); err == nil {
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

// f64s converts string to float64
func f64s(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
