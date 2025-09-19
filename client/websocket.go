package client

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type WSClient struct {
	conn          *websocket.Conn
	url           string
	assets        map[string]chan MarketUpdate
	mu            sync.RWMutex
	isShutdown    bool
	lastError     time.Time
	errorCount    int
	messageCount  int
	reconnectChan chan struct{}
}

type MarketUpdate struct {
	EventType string `json:"event_type"`
	AssetID   string `json:"asset_id"`
	Market    string `json:"market"`
	Timestamp string `json:"timestamp"`
	Hash      string `json:"hash"`

	Bids []PriceLevel `json:"bids,omitempty"`
	Asks []PriceLevel `json:"asks,omitempty"`

	Changes []PriceChange `json:"changes,omitempty"`

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

	log.Printf("WebSocket: Connecting to %s", url)
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if resp != nil {
			log.Printf("WebSocket: Connection failed with status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("websocket dial error: %w", err)
	}

	log.Println("WebSocket: Connected successfully")

	return &WSClient{
		conn:          conn,
		url:           url,
		assets:        make(map[string]chan MarketUpdate),
		reconnectChan: make(chan struct{}, 1),
	}, nil
}

// IsShutdown returns true if the client has shut down
func (ws *WSClient) IsShutdown() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.isShutdown
}

// IsConnected returns true if the WebSocket is connected
func (ws *WSClient) IsConnected() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.conn != nil && !ws.isShutdown
}

// Reconnect establishes a new connection
func (ws *WSClient) Reconnect() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Close old connection if exists
	if ws.conn != nil {
		ws.conn.Close()
	}

	log.Printf("WebSocket: Attempting reconnection to %s", ws.url)
	conn, resp, err := websocket.DefaultDialer.Dial(ws.url, nil)
	if err != nil {
		if resp != nil {
			log.Printf("WebSocket: Reconnection failed with status %d", resp.StatusCode)
		}
		return fmt.Errorf("websocket reconnect error: %w", err)
	}

	ws.conn = conn
	ws.isShutdown = false
	ws.errorCount = 0
	log.Println("WebSocket: Reconnected successfully")

	// Resubscribe to all assets
	for assetID := range ws.assets {
		sub := map[string]interface{}{
			"type":       "subscribe",
			"channel":    "market",
			"assets_ids": []string{assetID},
		}
		if err := ws.conn.WriteJSON(sub); err != nil {
			log.Printf("WebSocket: Failed to resubscribe to %s: %v", assetID[:20], err)
		} else {
			log.Printf("WebSocket: Resubscribed to %s", assetID[:20]+"...")
		}
	}

	return nil
}

func (ws *WSClient) Subscribe(assetID string) (<-chan MarketUpdate, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Check if already subscribed
	if ch, exists := ws.assets[assetID]; exists {
		return ch, nil
	}

	ch := make(chan MarketUpdate, 100)
	ws.assets[assetID] = ch

	// Only send subscription if connected
	if ws.conn != nil && !ws.isShutdown {
		sub := map[string]interface{}{
			"type":       "subscribe",
			"channel":    "market",
			"assets_ids": []string{assetID},
		}

		if err := ws.conn.WriteJSON(sub); err != nil {
			return nil, fmt.Errorf("subscribe error: %w", err)
		}
		log.Printf("WebSocket: Subscribed to %s", assetID[:20]+"...")
	} else {
		log.Printf("WebSocket: Queued subscription for %s (will subscribe on reconnect)", assetID[:20]+"...")
	}

	return ch, nil
}

func (ws *WSClient) Listen(ctx context.Context) {
	log.Println("WebSocket: Listener started")

	// Heartbeat ticker
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	// Error reset ticker - reset error count every minute if no new errors
	errorResetTicker := time.NewTicker(1 * time.Minute)
	defer errorResetTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("WebSocket: Listener stopping (received %d messages, %d errors)",
				ws.messageCount, ws.errorCount)
			ws.Close()
			return

		case <-heartbeatTicker.C:
			// Send ping to keep connection alive
			ws.mu.Lock()
			if ws.conn != nil && !ws.isShutdown {
				if err := ws.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Printf("WebSocket: Ping failed: %v", err)
					ws.handleError("ping failure")
				} else {
					log.Printf("WebSocket: Ping sent successfully")
				}
			}
			ws.mu.Unlock()

		case <-errorResetTicker.C:
			// Reset error count if no recent errors
			ws.mu.Lock()
			if time.Since(ws.lastError) > 1*time.Minute && ws.errorCount > 0 {
				log.Printf("WebSocket: No errors for 1 minute, resetting error count from %d", ws.errorCount)
				ws.errorCount = 0
			}
			ws.mu.Unlock()

		case <-ws.reconnectChan:
			// Handle reconnection request
			log.Println("WebSocket: Reconnection requested")
			if err := ws.Reconnect(); err != nil {
				log.Printf("WebSocket: Reconnection failed: %v", err)
				// Try again in 5 seconds
				time.Sleep(5 * time.Second)
				select {
				case ws.reconnectChan <- struct{}{}:
				default:
				}
			}

		default:
			// Check if we need to reconnect
			if !ws.IsConnected() {
				select {
				case ws.reconnectChan <- struct{}{}:
				default:
				}
				time.Sleep(1 * time.Second)
				continue
			}

			// Set read deadline
			ws.mu.RLock()
			conn := ws.conn
			ws.mu.RUnlock()

			if conn == nil {
				time.Sleep(1 * time.Second)
				continue
			}

			conn.SetReadDeadline(time.Now().Add(60 * time.Second))

			var messages []MarketUpdate
			err := conn.ReadJSON(&messages)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket: Connection closed unexpectedly: %v", err)
					ws.handleError("unexpected close")
					// Request reconnection
					select {
					case ws.reconnectChan <- struct{}{}:
					default:
					}
					continue
				}

				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Println("WebSocket: Normal close")
					ws.mu.Lock()
					ws.isShutdown = true
					ws.mu.Unlock()
					return
				}

				// Handle read timeout
				if netErr, ok := err.(*websocket.CloseError); ok {
					log.Printf("WebSocket: Close error %d: %s", netErr.Code, netErr.Text)
				} else {
					log.Printf("WebSocket: Read error: %v", err)
				}

				ws.handleError("read error")
				continue
			}

			// Successfully received messages
			ws.mu.Lock()
			ws.messageCount++
			messageCount := ws.messageCount
			ws.mu.Unlock()

			if messageCount <= 5 || messageCount%100 == 0 {
				log.Printf("WebSocket: Message #%d received (%d updates)",
					messageCount, len(messages))
			}

			// Process each message
			ws.processMessages(messages)
		}
	}
}

func (ws *WSClient) handleError(errorType string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.errorCount++
	ws.lastError = time.Now()

	log.Printf("WebSocket: Error #%d (%s)", ws.errorCount, errorType)

	// Only shut down after many consecutive errors in a short time
	if ws.errorCount > 20 && time.Since(ws.lastError) < 5*time.Minute {
		log.Printf("WebSocket: Too many errors (%d) in short period, marking for reconnection", ws.errorCount)
		ws.isShutdown = true
		if ws.conn != nil {
			ws.conn.Close()
			ws.conn = nil
		}
	}
}

func (ws *WSClient) processMessages(messages []MarketUpdate) {
	for _, msg := range messages {
		// Parse best bid/ask from book events
		if msg.EventType == "book" && len(msg.Bids) > 0 && len(msg.Asks) > 0 {
			if bid, err := strconv.ParseFloat(msg.Bids[0].Price, 64); err == nil {
				msg.BestBid = bid
			}
			if ask, err := strconv.ParseFloat(msg.Asks[0].Price, 64); err == nil {
				msg.BestAsk = ask
			}
		}

		// Route to appropriate channel
		ws.mu.RLock()
		ch, exists := ws.assets[msg.AssetID]
		messageCount := ws.messageCount
		ws.mu.RUnlock()

		if exists {
			select {
			case ch <- msg:
				if messageCount <= 5 {
					log.Printf("WebSocket: Routed update for %s (bid=%.4f, ask=%.4f)",
						msg.AssetID[:20], msg.BestBid, msg.BestAsk)
				}
			default:
				log.Printf("WebSocket: WARNING - Channel full for %s", msg.AssetID[:20])
			}
		} else {
			if messageCount <= 10 {
				log.Printf("WebSocket: No subscriber for asset %s", msg.AssetID[:20])
			}
		}
	}
}

func (ws *WSClient) Assets() map[string]chan MarketUpdate {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	// Return a copy to avoid race conditions
	assets := make(map[string]chan MarketUpdate)
	for k, v := range ws.assets {
		assets[k] = v
	}
	return assets
}

func (ws *WSClient) Close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.isShutdown = true

	if ws.conn != nil {
		log.Println("WebSocket: Closing connection")
		ws.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		err := ws.conn.Close()
		ws.conn = nil
		return err
	}
	return nil
}
