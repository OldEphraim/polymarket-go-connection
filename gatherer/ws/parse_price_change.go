package ws

// Top-level "gamma" shape (strings), e.g. {"price_changes":[...], "timestamp":"1730612345678"}
type gammaPriceChange struct {
	Market       string            `json:"market"`
	PriceChanges []gammaPCChange   `json:"price_changes"`
	Timestamp    string            `json:"timestamp"`  // ms as string
	EventType    string            `json:"event_type"` // "price_change"
	Extra        map[string]string `json:"extra,omitempty"`
}

type gammaPCChange struct {
	AssetID string `json:"asset_id"`
	Price   string `json:"price"`
	Size    string `json:"size"`
	Side    string `json:"side"`
	BestBid string `json:"best_bid"`
	BestAsk string `json:"best_ask"`
}

// Nested shape: {"token_id":"...", "price_changes":[{"best_bid":0.5,"best_ask":0.51}, ...]}
type priceChangeMsg struct {
	TokenID string       `json:"token_id"`
	Changes []bestChange `json:"price_changes"`
}
type bestChange struct {
	BestBid float64 `json:"best_bid"`
	BestAsk float64 `json:"best_ask"`
}
