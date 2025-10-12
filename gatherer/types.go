package gatherer

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/sqlc-dev/pqtype"
)

// ===== Existing event type (kept compatible) =====
type MarketEventType string

const (
	NewMarket      MarketEventType = "new_market"
	PriceJump      MarketEventType = "price_jump"
	VolumeSpike    MarketEventType = "volume_spike"
	LiquidityShift MarketEventType = "liquidity_shift"
	VolumeSurge    MarketEventType = "volume_surge"
	ImbalanceShift MarketEventType = "imbalance_shift"
	NewHigh        MarketEventType = "new_high"
	NewLow         MarketEventType = "new_low"
)

type MarketEvent struct {
	Type      MarketEventType        `json:"type"`
	TokenID   string                 `json:"token_id"`
	EventID   string                 `json:"event_id,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	OldValue  float64                `json:"old_value,omitempty"`
	NewValue  float64                `json:"new_value,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// ===== Normalized book & tape =====
type Quote struct {
	TokenID   string
	TS        time.Time
	BestBid   float64
	BestAsk   float64
	BidSize1  float64
	AskSize1  float64
	SpreadBps float64
	Mid       float64
}

type Trade struct {
	TokenID   string
	TS        time.Time
	Price     float64
	Size      float64
	Aggressor string // "buy" or "sell"
	TradeID   string // optional if available
}

// ===== Derived features snapshot =====
type FeatureUpdate struct {
	TokenID        string
	TS             time.Time
	Ret1m          float64
	Ret5m          float64
	Vol1m          float64
	AvgVol5m       float64
	Sigma5m        float64
	ZScore5m       float64
	ImbalanceTop   float64
	SpreadBps      float64
	BrokeHigh15m   bool
	BrokeLow15m    bool
	TimeToResolveH float64
	SignedFlow1m   float64 // +buy -sell
}

// ===== DB-facing types (align with your sqlc) =====
type UpsertMarketScanParams struct {
	TokenID      string
	EventID      sql.NullString
	Slug         sql.NullString
	Question     sql.NullString
	LastPrice    sql.NullFloat64
	LastVolume   sql.NullFloat64
	Liquidity    sql.NullFloat64
	Metadata     pqtype.NullRawMessage
	Price24hAgo  sql.NullFloat64
	Volume24hAgo sql.NullFloat64
}

type UpsertMarketParams struct {
	TokenID  string
	Slug     sql.NullString
	Question sql.NullString
	Outcome  sql.NullString
}

type RecordMarketEventParams struct {
	TokenID   string
	EventType sql.NullString
	OldValue  sql.NullFloat64
	NewValue  sql.NullFloat64
	Metadata  pqtype.NullRawMessage
}

// Gamma /ws/market price_change frame (top-level, numbers as strings)
type gammaPriceChange struct {
	Market       string            `json:"market"`
	PriceChanges []gammaPCChange   `json:"price_changes"`
	Timestamp    string            `json:"timestamp"`  // ms as string
	EventType    string            `json:"event_type"` // "price_change"
	Extra        map[string]string `json:"extra,omitempty"`
}

type gammaPCChange struct {
	AssetID string `json:"asset_id"`
	Price   string `json:"price"`    // trade-ish price
	Size    string `json:"size"`     // "0" if just a quote move
	Side    string `json:"side"`     // "BUY" / "SELL"
	BestBid string `json:"best_bid"` // top-of-book as strings
	BestAsk string `json:"best_ask"`
}

// Top-level price_change shape the server is sending us
type priceChangeTop struct {
	Market       string `json:"market"`
	EventType    string `json:"event_type"`
	TimestampStr string `json:"timestamp"` // often stringified millis
	PriceChanges []struct {
		AssetID string `json:"asset_id"`
		// best_* come as strings; parse to float
		BestBid string `json:"best_bid"`
		BestAsk string `json:"best_ask"`
		// other fields we don't need right now:
		Price string `json:"price"`
		Size  string `json:"size"`
		Side  string `json:"side"`
		Hash  string `json:"hash"`
	} `json:"price_changes"`
}

// ===== Store interface (backed by your sqlc-generated Store) =====
type Store interface {
	UpsertMarketScan(ctx context.Context, p UpsertMarketScanParams) (MarketScanRow, error)
	UpsertMarket(ctx context.Context, p UpsertMarketParams) (interface{}, error)
	RecordMarketEvent(ctx context.Context, p RecordMarketEventParams) (interface{}, error)

	// New tables (quotes/trades/features) — fill with your sqlc names
	InsertQuote(ctx context.Context, q Quote) error            // TODO(sqlc)
	InsertTrade(ctx context.Context, t Trade) error            // TODO(sqlc)
	UpsertFeatures(ctx context.Context, f FeatureUpdate) error // TODO(sqlc)

	// Convenience
	GetActiveMarketScans(ctx context.Context, limit int) ([]MarketScanRow, error)
}

// Minimal projection of your sqlc MarketScan row.
type MarketScanRow struct {
	TokenID      string
	EventID      sql.NullString
	Slug         sql.NullString
	Question     sql.NullString
	LastPrice    sql.NullFloat64
	LastVolume   sql.NullFloat64
	Liquidity    sql.NullFloat64
	Metadata     pqtype.NullRawMessage
	Price24hAgo  sql.NullFloat64
	Volume24hAgo sql.NullFloat64
}

// ===== Polymarket types =====
type PolymarketEvent struct {
	ID        string             `json:"id"`
	Slug      string             `json:"slug"`
	Title     string             `json:"title"`
	StartDate *time.Time         `json:"startDate"`
	EndDate   *time.Time         `json:"endDate"`
	Markets   []PolymarketMarket `json:"markets"`
}

type PolymarketMarket struct {
	ID          string     `json:"id"`
	Question    string     `json:"question"`
	ConditionID string     `json:"conditionId"`
	QuestionID  string     `json:"questionID"`
	Slug        string     `json:"slug"`
	Closed      bool       `json:"closed"`
	CreatedAt   *time.Time `json:"createdAt"`

	// Prices / top-of-book (Gamma)
	LastTradePrice float64 `json:"lastTradePrice"`
	BestBid        float64 `json:"bestBid"`
	BestAsk        float64 `json:"bestAsk"`

	// Liquidity / volume (Gamma)
	VolumeNum    float64 `json:"volumeNum"` // total volume (numeric)
	LiquidityNum float64 `json:"liquidityNum"`
	Volume24hr   float64 `json:"volume24hr"` // 24h flow

	ClobTokenIds string `json:"clobTokenIds"`

	// Nice-to-have (present in docs; safe to omit if absent)
	OutcomePrices json.RawMessage `json:"outcomePrices,omitempty"`
}
