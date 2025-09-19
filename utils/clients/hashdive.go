package clients

import (
	"fmt"
	"time"
)

// HashdiveAPI provides methods for interacting with Hashdive's whale tracking API
type HashdiveAPI struct {
	client *HTTPClient
	apiKey string
}

// NewHashdiveAPI creates a new Hashdive API client
func NewHashdiveAPI(apiKey string) *HashdiveAPI {
	return &HashdiveAPI{
		client: NewHTTPClient(30 * time.Second),
		apiKey: apiKey,
	}
}

// WhaleTrade represents a large trade from Hashdive
type WhaleTrade struct {
	Timestamp   int64   `json:"timestamp"`
	UserAddress string  `json:"user_address"`
	AssetID     string  `json:"asset_id"`
	Side        string  `json:"side"`
	Price       float64 `json:"price"`
	Size        float64 `json:"size"`
	USDSize     float64 `json:"usd_size"`
	MarketInfo  struct {
		Question string `json:"question"`
		Outcome  string `json:"outcome"`
	} `json:"market_info"`
}

// GetWhaleTrades fetches recent whale trades above a minimum USD size
func (h *HashdiveAPI) GetWhaleTrades(minUSD float64, limit int) ([]WhaleTrade, error) {
	url := fmt.Sprintf("https://hashdive.com/api/get_latest_whale_trades?min_usd=%.0f&limit=%d",
		minUSD, limit)

	headers := map[string]string{
		"x-api-key": h.apiKey,
	}

	var trades []WhaleTrade
	err := h.client.MakeJSONRequest(url, headers, &trades)
	if err != nil {
		return nil, fmt.Errorf("failed to get whale trades: %w", err)
	}

	return trades, nil
}

// WhalePosition represents a whale's position in a market
type WhalePosition struct {
	UserAddress string  `json:"user_address"`
	AssetID     string  `json:"asset_id"`
	Amount      float64 `json:"amount"`
	AvgPrice    float64 `json:"avgPrice"`
}

// GetWhalePosition fetches a specific whale's position in a market
func (h *HashdiveAPI) GetWhalePosition(userAddress, assetID string) (*WhalePosition, error) {
	url := fmt.Sprintf("https://hashdive.com/api/get_positions?user_address=%s&asset_id=%s",
		userAddress, assetID)

	headers := map[string]string{
		"x-api-key": h.apiKey,
	}

	var positions []WhalePosition
	err := h.client.MakeJSONRequest(url, headers, &positions)
	if err != nil {
		return nil, fmt.Errorf("failed to get whale position: %w", err)
	}

	if len(positions) > 0 {
		return &positions[0], nil
	}

	return nil, nil
}

// GetWhalePositions fetches all positions for a specific whale
func (h *HashdiveAPI) GetWhalePositions(userAddress string) ([]WhalePosition, error) {
	url := fmt.Sprintf("https://hashdive.com/api/get_positions?user_address=%s", userAddress)

	headers := map[string]string{
		"x-api-key": h.apiKey,
	}

	var positions []WhalePosition
	err := h.client.MakeJSONRequest(url, headers, &positions)
	if err != nil {
		return nil, fmt.Errorf("failed to get whale positions: %w", err)
	}

	return positions, nil
}
