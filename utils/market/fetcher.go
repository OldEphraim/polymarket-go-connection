package market

import (
	"github.com/OldEphraim/polymarket-go-connection/client"
)

// Fetcher provides methods for fetching market data and subscribing to updates
type Fetcher interface {
	GetOrderBook(tokenID string) (map[string]interface{}, error)
	Subscribe(tokenID string) (<-chan client.MarketUpdate, error)
}

// StandardFetcher wraps the Polymarket client to implement Fetcher
type StandardFetcher struct {
	pmClient *client.PolymarketClient
	wsClient *client.WSClient
}

// NewStandardFetcher creates a new market fetcher using Polymarket clients
func NewStandardFetcher(pmClient *client.PolymarketClient, wsClient *client.WSClient) *StandardFetcher {
	return &StandardFetcher{
		pmClient: pmClient,
		wsClient: wsClient,
	}
}

// GetOrderBook fetches the order book for a specific token
func (s *StandardFetcher) GetOrderBook(tokenID string) (map[string]interface{}, error) {
	return s.pmClient.GetOrderBook(tokenID)
}

// Subscribe subscribes to market updates for a specific token
func (s *StandardFetcher) Subscribe(tokenID string) (<-chan client.MarketUpdate, error) {
	return s.wsClient.Subscribe(tokenID)
}
