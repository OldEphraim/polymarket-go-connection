package websocket

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/client"
)

// Manager handles WebSocket connection management and reconnection
type Manager struct {
	wsClient      *client.WSClient
	mu            sync.RWMutex
	subscriptions map[string]bool // Track what we're subscribed to
	callbacks     map[string]func(client.MarketUpdate)
	reconnectWait time.Duration
	maxRetries    int
}

// NewManager creates a new WebSocket manager
func NewManager(wsClient *client.WSClient) *Manager {
	return &Manager{
		wsClient:      wsClient,
		subscriptions: make(map[string]bool),
		callbacks:     make(map[string]func(client.MarketUpdate)),
		reconnectWait: 5 * time.Second,
		maxRetries:    10,
	}
}

// MaintainConnection keeps the WebSocket connection alive with reconnection logic
func (m *Manager) MaintainConnection(ctx context.Context) {
	retryCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Check if WebSocket is alive
			if m.isConnected() {
				time.Sleep(10 * time.Second)
				retryCount = 0 // Reset retry count on successful connection
				continue
			}

			// Try to reconnect
			retryCount++
			if retryCount > m.maxRetries {
				log.Printf("WebSocket: Max reconnection attempts reached, waiting 5 minutes")
				time.Sleep(5 * time.Minute)
				retryCount = 0
				continue
			}

			log.Printf("WebSocket: Attempting reconnection (attempt %d/%d)", retryCount, m.maxRetries)
			if err := m.reconnect(ctx); err != nil {
				log.Printf("WebSocket: Reconnection failed: %v", err)
				time.Sleep(time.Duration(retryCount) * m.reconnectWait)
				continue
			}

			log.Println("WebSocket: Reconnected successfully")
			m.resubscribeAll()
		}
	}
}

// Subscribe subscribes to a token and registers a callback
func (m *Manager) Subscribe(tokenID string, callback func(client.MarketUpdate)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Subscribe through the WebSocket client
	updates, err := m.wsClient.Subscribe(tokenID)
	if err != nil {
		return err
	}

	// Track subscription and callback
	m.subscriptions[tokenID] = true
	m.callbacks[tokenID] = callback

	// Start monitoring in a goroutine
	go m.monitorUpdates(tokenID, updates, callback)

	return nil
}

// SubscribeWithReconnect subscribes and ensures the subscription survives reconnections
func (m *Manager) SubscribeWithReconnect(ctx context.Context, tokenID string, callback func(client.MarketUpdate)) error {
	if err := m.Subscribe(tokenID, callback); err != nil {
		return err
	}

	// This subscription will be automatically restored on reconnection
	return nil
}

// Unsubscribe removes a subscription
func (m *Manager) Unsubscribe(tokenID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.subscriptions, tokenID)
	delete(m.callbacks, tokenID)

	// Note: The actual WebSocket unsubscribe would depend on your client implementation
}

// MonitorPriceUpdates is a simpler interface for just monitoring prices
func (m *Manager) MonitorPriceUpdates(tokenID string, callback func(update client.MarketUpdate)) {
	m.Subscribe(tokenID, callback)
}

// GetActiveSubscriptions returns the list of active subscriptions
func (m *Manager) GetActiveSubscriptions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var subs []string
	for tokenID := range m.subscriptions {
		subs = append(subs, tokenID)
	}
	return subs
}

// Private methods

func (m *Manager) isConnected() bool {
	// This depends on your WebSocket client implementation
	// You might need to add a method to check connection status
	// For now, we'll assume it's connected if the client exists
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.wsClient != nil
}

func (m *Manager) reconnect(ctx context.Context) error {
	// Create a new WebSocket client
	wsClient, err := client.NewWSClient()
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.wsClient = wsClient
	m.mu.Unlock()

	// Start listening
	go m.wsClient.Listen(ctx)

	return nil
}

func (m *Manager) resubscribeAll() {
	m.mu.RLock()
	subs := make(map[string]func(client.MarketUpdate))
	for tokenID, callback := range m.callbacks {
		subs[tokenID] = callback
	}
	m.mu.RUnlock()

	// Resubscribe to all tokens
	for tokenID, callback := range subs {
		log.Printf("WebSocket: Resubscribing to %s", tokenID[:20])
		if err := m.Subscribe(tokenID, callback); err != nil {
			log.Printf("WebSocket: Failed to resubscribe to %s: %v", tokenID[:20], err)
		}
	}
}

func (m *Manager) monitorUpdates(tokenID string, updates <-chan client.MarketUpdate, callback func(client.MarketUpdate)) {
	for update := range updates {
		// Check if we're still subscribed
		m.mu.RLock()
		_, stillSubscribed := m.subscriptions[tokenID]
		m.mu.RUnlock()

		if !stillSubscribed {
			return
		}

		// Call the callback with the update
		callback(update)
	}
}
