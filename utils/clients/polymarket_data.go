package clients

import (
	"fmt"
	"time"
)

// PolymarketDataAPI provides methods for interacting with Polymarket's data API
type PolymarketDataAPI struct {
	client *HTTPClient
}

// NewPolymarketDataAPI creates a new Polymarket data API client
func NewPolymarketDataAPI() *PolymarketDataAPI {
	return &PolymarketDataAPI{
		client: NewHTTPClient(10 * time.Second),
	}
}

// UserActivity represents a user's trading activity on Polymarket
type UserActivity struct {
	ProxyWallet     string  `json:"proxyWallet"`
	Timestamp       int64   `json:"timestamp"`
	ConditionID     string  `json:"conditionId"`
	Type            string  `json:"type"`
	Size            float64 `json:"size"`
	USDCSize        float64 `json:"usdcSize"`
	TransactionHash string  `json:"transactionHash"`
	Price           float64 `json:"price"`
	Asset           string  `json:"asset"`
	Side            string  `json:"side"`
	OutcomeIndex    int     `json:"outcomeIndex"`
	Title           string  `json:"title"`
	Slug            string  `json:"slug"`
	EventSlug       string  `json:"eventSlug"`
	Outcome         string  `json:"outcome"`
}

// GetUserActivity fetches recent activity for a specific user address
func (p *PolymarketDataAPI) GetUserActivity(address string, limit int) ([]UserActivity, error) {
	url := fmt.Sprintf("https://data-api.polymarket.com/activity?user=%s&limit=%d&sortBy=TIMESTAMP&sortDirection=DESC",
		address, limit)

	var activities []UserActivity
	err := p.client.MakeJSONRequest(url, nil, &activities)
	if err != nil {
		return nil, fmt.Errorf("failed to get user activity: %w", err)
	}

	return activities, nil
}

// GetUserActivitySince fetches user activity since a specific timestamp
func (p *PolymarketDataAPI) GetUserActivitySince(address string, since int64, limit int) ([]UserActivity, error) {
	url := fmt.Sprintf("https://data-api.polymarket.com/activity?user=%s&limit=%d&since=%d&sortBy=TIMESTAMP&sortDirection=DESC",
		address, limit, since)

	var activities []UserActivity
	err := p.client.MakeJSONRequest(url, nil, &activities)
	if err != nil {
		return nil, fmt.Errorf("failed to get user activity: %w", err)
	}

	return activities, nil
}
