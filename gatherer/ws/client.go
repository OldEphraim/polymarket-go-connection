package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// Client is the public interface the gatherer uses.
type Client interface {
	Run(
		ctx context.Context,
		url string,
		assetIDs []string,
		onQuote func(assetID string, bestBid, bestAsk float64, ts time.Time),
		onTrade func(assetID string, price float64, side string, size float64, ts time.Time),
	) error
}

func NewPolymarketClient(log *slog.Logger) Client {
	if log == nil {
		log = slog.Default()
	}
	return &polyWS{log: log}
}

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
		c.log.Info("ws connecting", "url", url, "assets", len(assetIDs), "sample", first)
	}

	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		if resp != nil && wsDebug {
			c.log.Warn("ws dial failed", "url", url, "status", resp.StatusCode, "err", err, "body", tryReadBody(resp))
		}
		return err
	}
	defer conn.Close()

	// Subscribe: {"type":"market","assets_ids":[...]}
	sub := map[string]any{"type": "market", "assets_ids": assetIDs}
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
			asset := firstNonEmpty(b.TokenID, b.AssetID)
			if asset == "" {
				if wsLogUnknown {
					writeUnknown("book_no_asset", data)
				}
				continue
			}
			bid, ask := topOfBook(b.Bids), topOfBook(b.Asks)
			if bid > 0 && ask > 0 && onQuote != nil {
				onQuote(asset, bid, ask, time.Now())
			}

		case "price_change":
			// Variant A: gamma top-level with string fields
			var gpc gammaPriceChange
			if err := json.Unmarshal(data, &gpc); err == nil && len(gpc.PriceChanges) > 0 {
				ts := time.Now()
				if ms, err := strconv.ParseInt(gpc.Timestamp, 10, 64); err == nil && ms > 0 {
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
			// Variant B: nested payload
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

		case "last_trade_price", "last_trade", "trade", "trades":
			raw := firstPresent(env["data"], env["payload"])
			if len(raw) == 0 {
				if v, ok := env["trades"]; ok {
					raw = v
				}
			}
			if len(raw) == 0 {
				raw = data
			}

			// frame-level timestamp (string milliseconds) if present
			fallbackTS := time.Now()
			if s := readString(env, "timestamp"); s != "" {
				if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 0 {
					fallbackTS = time.Unix(0, ms*int64(time.Millisecond))
				}
			}

			trades := extractTrades(raw)
			if len(trades) == 0 {
				if wsLogUnknown {
					writeUnknown("trade_unknown_"+string(et), data)
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
