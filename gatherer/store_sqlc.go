package gatherer

import (
	"context"
	"database/sql"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type SQLCStore struct {
	q  *database.Queries
	db *sql.DB
}

func NewSQLCStore(q *database.Queries, db *sql.DB) *SQLCStore {
	return &SQLCStore{q: q, db: db}
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
