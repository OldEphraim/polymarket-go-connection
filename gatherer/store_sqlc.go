package gatherer

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
	"github.com/sqlc-dev/pqtype"
)

type SQLCStore struct {
	q *database.Queries
}

func NewSQLCStore(q *database.Queries) *SQLCStore { return &SQLCStore{q: q} }

// ---- Helpers ----
func nff(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: !math.IsNaN(v)} }
func ns(s string) sql.NullString    { return sql.NullString{String: s, Valid: s != ""} }
func njson(m map[string]any) pqtype.NullRawMessage {
	b := jsonMarshal(m) // small wrapper to avoid import cycles
	return pqtype.NullRawMessage{RawMessage: b, Valid: b != nil}
}

// jsonMarshal is isolated to avoid importing encoding/json here if you prefer.
func jsonMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---- Implement gatherer.Store ----

// Existing tables
func (s *SQLCStore) UpsertMarketScan(ctx context.Context, p UpsertMarketScanParams) (MarketScanRow, error) {
	row, err := s.q.UpsertMarketScan(ctx, database.UpsertMarketScanParams{
		TokenID:      p.TokenID,
		EventID:      p.EventID,
		Slug:         p.Slug,
		Question:     p.Question,
		LastPrice:    p.LastPrice,
		LastVolume:   p.LastVolume,
		Liquidity:    p.Liquidity,
		Price24hAgo:  p.Price24hAgo,  // sqlc will camelize as Price_24hAgo
		Volume24hAgo: p.Volume24hAgo, // same note
		Metadata:     p.Metadata,
	})
	if err != nil {
		return MarketScanRow{}, err
	}
	// map generated row → minimal projection expected by gatherer
	return MarketScanRow{
		TokenID:      row.TokenID,
		EventID:      row.EventID,
		Slug:         row.Slug,
		Question:     row.Question,
		LastPrice:    row.LastPrice,
		LastVolume:   row.LastVolume,
		Liquidity:    row.Liquidity,
		Metadata:     row.Metadata,
		Price24hAgo:  row.Price24hAgo,
		Volume24hAgo: row.Volume24hAgo,
	}, nil
}

func (s *SQLCStore) UpsertMarket(ctx context.Context, p UpsertMarketParams) (any, error) {
	_, err := s.q.UpsertMarket(ctx, database.UpsertMarketParams{
		TokenID:  p.TokenID,
		Slug:     p.Slug,
		Question: p.Question,
		Outcome:  p.Outcome,
	})
	return nil, err
}

func (s *SQLCStore) RecordMarketEvent(ctx context.Context, p RecordMarketEventParams) (any, error) {
	_, err := s.q.RecordMarketEvent(ctx, database.RecordMarketEventParams{
		TokenID:   p.TokenID,
		EventType: p.EventType,
		OldValue:  p.OldValue,
		NewValue:  p.NewValue,
		Metadata:  p.Metadata,
	})
	return nil, err
}

func (s *SQLCStore) GetActiveMarketScans(ctx context.Context, limit int) ([]MarketScanRow, error) {
	rows, err := s.q.GetActiveMarketScans(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]MarketScanRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, MarketScanRow{
			TokenID:      r.TokenID,
			EventID:      r.EventID,
			Slug:         r.Slug,
			Question:     r.Question,
			LastPrice:    r.LastPrice,
			LastVolume:   r.LastVolume,
			Liquidity:    r.Liquidity,
			Metadata:     r.Metadata,
			Price24hAgo:  r.Price24hAgo,
			Volume24hAgo: r.Volume24hAgo,
		})
	}
	return out, nil
}

// New tables
func (s *SQLCStore) InsertQuote(ctx context.Context, q Quote) error {
	return s.q.InsertQuote(ctx, database.InsertQuoteParams{
		TokenID:   q.TokenID,
		Ts:        q.TS,
		BestBid:   nff(q.BestBid),
		BestAsk:   nff(q.BestAsk),
		BidSize1:  nff(q.BidSize1),
		AskSize1:  nff(q.AskSize1),
		SpreadBps: nff(q.SpreadBps),
		Mid:       nff(q.Mid),
	})
}

func (s *SQLCStore) InsertTrade(ctx context.Context, t Trade) error {
	return s.q.InsertTrade(ctx, database.InsertTradeParams{
		TokenID:   t.TokenID,
		Ts:        t.TS,
		Price:     t.Price,
		Size:      t.Size,
		Aggressor: ns(t.Aggressor),
		TradeID:   ns(t.TradeID),
	})
}

func (s *SQLCStore) UpsertFeatures(ctx context.Context, f FeatureUpdate) error {
	return s.q.UpsertFeatures(ctx, database.UpsertFeaturesParams{
		TokenID:        f.TokenID,
		Ts:             f.TS,
		Ret1m:          nff(f.Ret1m),
		Ret5m:          nff(f.Ret5m),
		Vol1m:          nff(f.Vol1m),
		AvgVol5m:       nff(f.AvgVol5m),
		Sigma5m:        nff(f.Sigma5m),
		Zscore5m:       nff(f.ZScore5m),
		ImbalanceTop:   nff(f.ImbalanceTop),
		SpreadBps:      nff(f.SpreadBps),
		BrokeHigh15m:   sql.NullBool{Bool: f.BrokeHigh15m, Valid: true},
		BrokeLow15m:    sql.NullBool{Bool: f.BrokeLow15m, Valid: true},
		TimeToResolveH: nff(f.TimeToResolveH),
		SignedFlow1m:   nff(f.SignedFlow1m),
	})
}

// Convenient extra reads (optional)
func (s *SQLCStore) GetLatestMid(ctx context.Context, tokenID string) (float64, error) {
	mid, err := s.q.GetLatestMid(ctx, tokenID)
	if err != nil {
		return 0, err
	}
	if !mid.Valid {
		return 0, sql.ErrNoRows
	}
	return mid.Float64, nil
}

func (s *SQLCStore) GetActiveTokenIDs(ctx context.Context, limit int) ([]string, error) {
	return s.q.GetActiveTokenIDs(ctx, int32(limit))
}
