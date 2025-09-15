package client

import (
	"context"
	"fmt"
	"log"

	"github.com/gorilla/websocket"
)

type WSClient struct {
	conn   *websocket.Conn
	url    string
	assets map[string]chan MarketUpdate
}

type MarketUpdate struct {
	EventType string `json:"event_type"`
	AssetID   string `json:"asset_id"`
	Market    string `json:"market"`
	Timestamp string `json:"timestamp"`
	Hash      string `json:"hash"`

	// For book events
	Bids []PriceLevel `json:"bids,omitempty"`
	Asks []PriceLevel `json:"asks,omitempty"`

	// For price_change events
	Changes []PriceChange `json:"changes,omitempty"`

	// Computed fields
	BestBid float64
	BestAsk float64
}

type PriceLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

type PriceChange struct {
	Price string `json:"price"`
	Side  string `json:"side"`
	Size  string `json:"size"`
}

func NewWSClient() (*WSClient, error) {
	url := "wss://ws-subscriptions-clob.polymarket.com/ws/market"

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}

	return &WSClient{
		conn:   conn,
		url:    url,
		assets: make(map[string]chan MarketUpdate),
	}, nil
}

func (ws *WSClient) Subscribe(assetID string) (<-chan MarketUpdate, error) {
	ch := make(chan MarketUpdate, 100)
	ws.assets[assetID] = ch

	sub := map[string]interface{}{
		"type":       "subscribe",
		"channel":    "market",
		"assets_ids": []string{assetID},
	}

	if err := ws.conn.WriteJSON(sub); err != nil {
		return nil, err
	}

	log.Printf("Subscribed to asset: %s", assetID[:20]+"...")
	return ch, nil
}

func (ws *WSClient) Listen(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			var messages []MarketUpdate
			err := ws.conn.ReadJSON(&messages)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				return
			}

			// Process each message in the array
			for _, msg := range messages {
				// Calculate best bid/ask
				if msg.EventType == "book" && len(msg.Bids) > 0 && len(msg.Asks) > 0 {
					// Parse first bid/ask as best prices
					var bidPrice, askPrice float64
					fmt.Sscanf(msg.Bids[0].Price, "%f", &bidPrice)
					fmt.Sscanf(msg.Asks[0].Price, "%f", &askPrice)
					msg.BestBid = bidPrice
					msg.BestAsk = askPrice
				}

				// Route to appropriate channel
				if ch, exists := ws.assets[msg.AssetID]; exists {
					select {
					case ch <- msg:
					default:
						log.Printf("Channel full for %s", msg.AssetID[:20])
					}
				}
			}
		}
	}
}

func (ws *WSClient) Close() error {
	if ws.conn != nil {
		return ws.conn.Close()
	}
	return nil
}
