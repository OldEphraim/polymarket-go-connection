package gatherer

import (
	"encoding/json"
	"strconv"
	"time"
)

// MarketEvent represents an event detected by the gatherer
type MarketEvent struct {
	Type      EventType
	TokenID   string
	EventID   string
	Timestamp time.Time
	OldValue  float64
	NewValue  float64
	Metadata  map[string]interface{}
}

// EventType represents the type of market event
type EventType string

const (
	PriceJump      EventType = "price_jump"
	VolumeSpike    EventType = "volume_spike"
	NewMarket      EventType = "new_market"
	LiquidityShift EventType = "liquidity_shift"
	MarketClosed   EventType = "market_closed"
)

// Tag represents a tag object from the API
type Tag struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// PolymarketEvent represents an event from the API
type PolymarketEvent struct {
	ID          string             `json:"id"`
	Slug        string             `json:"slug"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Markets     []PolymarketMarket `json:"markets"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	StartDate   time.Time          `json:"startDate"`
	EndDate     time.Time          `json:"endDate"`
	Closed      bool               `json:"closed"`
	Active      bool               `json:"active"`
	Tags        []Tag              `json:"tags"`
	Liquidity   float64            `json:"liquidity"`
	Volume      float64            `json:"volume"`
	Volume24hr  float64            `json:"volume24hr"`
}

// PolymarketMarket represents a market from the API
type PolymarketMarket struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Question    string    `json:"question"`
	GroupID     string    `json:"groupId,omitempty"`
	ConditionID string    `json:"conditionId"`
	QuestionID  string    `json:"questionID"`
	Description string    `json:"description"`
	Active      bool      `json:"active"`
	Closed      bool      `json:"closed"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	StartDate   time.Time `json:"startDate"`
	EndDate     time.Time `json:"endDate"`

	// Price fields
	BestBid        float64 `json:"bestBid"`
	BestAsk        float64 `json:"bestAsk"`
	LastTradePrice float64 `json:"lastTradePrice"`
	OutcomePrices  string  `json:"outcomePrices"` // JSON string array like "[\"0.0245\", \"0.9755\"]"
	Outcomes       string  `json:"outcomes"`      // JSON string array like "[\"Yes\", \"No\"]"

	// Volume and liquidity - can be string or number
	Volume       interface{} `json:"volume"` // Can be string or float
	Volume24hr   interface{} `json:"volume24hr"`
	Volume1wk    interface{} `json:"volume1wk"`
	Volume1mo    interface{} `json:"volume1mo"`
	Liquidity    interface{} `json:"liquidity"` // Can be string or float
	LiquidityNum float64     `json:"liquidityNum"`
	VolumeNum    float64     `json:"volumeNum"`

	// Other fields
	Spread       float64 `json:"spread"`
	OrderMinSize float64 `json:"orderMinSize"`
}

// Helper methods to safely get numeric values
func (m PolymarketMarket) GetVolume() float64 {
	return parseInterface(m.Volume)
}

func (m PolymarketMarket) GetVolume24hr() float64 {
	return parseInterface(m.Volume24hr)
}

func (m PolymarketMarket) GetLiquidity() float64 {
	return parseInterface(m.Liquidity)
}

func (m PolymarketMarket) GetOutcomePrices() []float64 {
	// Parse JSON string array like "[\"0.0245\", \"0.9755\"]"
	if m.OutcomePrices == "" {
		return []float64{}
	}

	var prices []string
	if err := json.Unmarshal([]byte(m.OutcomePrices), &prices); err != nil {
		return []float64{}
	}

	result := make([]float64, len(prices))
	for i, p := range prices {
		result[i] = parseFloat(p)
	}
	return result
}

// parseInterface converts interface{} (string or float64) to float64
func parseInterface(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case string:
		return parseFloat(val)
	case nil:
		return 0
	default:
		return 0
	}
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
