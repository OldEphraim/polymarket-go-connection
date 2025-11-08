package archiver

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type Dumper interface {
	DumpHour(ctx context.Context, w Window, out io.Writer) (rows int64, err error)
}

type FeaturesDumper struct{ Q *database.Queries }
type TradesDumper struct{ Q *database.Queries }
type QuotesDumper struct{ Q *database.Queries }

func (d *FeaturesDumper) DumpHour(ctx context.Context, w Window, out io.Writer) (int64, error) {
	rs, err := d.Q.DumpFeaturesHour(ctx, database.DumpFeaturesHourParams{
		TsStart: sql.NullTime{Time: w.Start, Valid: true},
		TsEnd:   sql.NullTime{Time: w.End, Valid: true},
	})
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(out)
	var n int64
	for _, r := range rs {
		rec := struct {
			TokenID        string    `json:"token_id"`
			TS             time.Time `json:"ts"`
			Ret1m          *float64  `json:"ret_1m,omitempty"`
			Ret5m          *float64  `json:"ret_5m,omitempty"`
			Vol1m          *float64  `json:"vol_1m,omitempty"`
			AvgVol5m       *float64  `json:"avg_vol_5m,omitempty"`
			Sigma5m        *float64  `json:"sigma_5m,omitempty"`
			Zscore5m       *float64  `json:"zscore_5m,omitempty"`
			ImbalanceTop   *float64  `json:"imbalance_top,omitempty"`
			SpreadBps      *float64  `json:"spread_bps,omitempty"`
			BrokeHigh15m   *bool     `json:"broke_high_15m,omitempty"`
			BrokeLow15m    *bool     `json:"broke_low_15m,omitempty"`
			TimeToResolveH *float64  `json:"time_to_resolve_h,omitempty"`
			SignedFlow1m   *float64  `json:"signed_flow_1m,omitempty"`
		}{
			TokenID: r.TokenID, TS: r.Ts.Time.UTC(),
			Ret1m: nullableF(r.Ret1m), Ret5m: nullableF(r.Ret5m), Vol1m: nullableF(r.Vol1m),
			AvgVol5m: nullableF(r.AvgVol5m), Sigma5m: nullableF(r.Sigma5m), Zscore5m: nullableF(r.Zscore5m),
			ImbalanceTop: nullableF(r.ImbalanceTop), SpreadBps: nullableF(r.SpreadBps),
			BrokeHigh15m: nullableB(r.BrokeHigh15m), BrokeLow15m: nullableB(r.BrokeLow15m),
			TimeToResolveH: nullableF(r.TimeToResolveH), SignedFlow1m: nullableF(r.SignedFlow1m),
		}
		if err := enc.Encode(rec); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (d *TradesDumper) DumpHour(ctx context.Context, w Window, out io.Writer) (int64, error) {
	rs, err := d.Q.DumpTradesHour(ctx, database.DumpTradesHourParams{
		TsStart: sql.NullTime{Time: w.Start, Valid: true},
		TsEnd:   sql.NullTime{Time: w.End, Valid: true},
	})
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(out)
	var n int64
	for _, r := range rs {
		rec := struct {
			TokenID   string    `json:"token_id"`
			TS        time.Time `json:"ts"`
			Price     float64   `json:"price"`
			Size      float64   `json:"size"`
			Aggressor *string   `json:"aggressor,omitempty"`
			TradeID   *string   `json:"trade_id,omitempty"`
		}{
			TokenID: r.TokenID, TS: r.Ts.Time.UTC(),
			Price: r.Price, Size: r.Size,
			Aggressor: nullableS(r.Aggressor), TradeID: nullableS(r.TradeID),
		}
		if err := enc.Encode(rec); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (d *QuotesDumper) DumpHour(ctx context.Context, w Window, out io.Writer) (int64, error) {
	rs, err := d.Q.DumpQuotesHour(ctx, database.DumpQuotesHourParams{
		TsStart: sql.NullTime{Time: w.Start, Valid: true},
		TsEnd:   sql.NullTime{Time: w.End, Valid: true},
	})
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(out)
	var n int64
	for _, r := range rs {
		rec := struct {
			TokenID   string    `json:"token_id"`
			TS        time.Time `json:"ts"`
			BestBid   *float64  `json:"best_bid,omitempty"`
			BestAsk   *float64  `json:"best_ask,omitempty"`
			BidSize1  *float64  `json:"bid_size1,omitempty"`
			AskSize1  *float64  `json:"ask_size1,omitempty"`
			SpreadBps *float64  `json:"spread_bps,omitempty"`
			Mid       *float64  `json:"mid,omitempty"`
		}{
			TokenID: r.TokenID, TS: r.Ts.Time.UTC(),
			BestBid: nullableF(r.BestBid), BestAsk: nullableF(r.BestAsk),
			BidSize1: nullableF(r.BidSize1), AskSize1: nullableF(r.AskSize1),
			SpreadBps: nullableF(r.SpreadBps), Mid: nullableF(r.Mid),
		}
		if err := enc.Encode(rec); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func nullableF(nf sql.NullFloat64) *float64 {
	if !nf.Valid {
		return nil
	}
	return &nf.Float64
}
func nullableB(nb sql.NullBool) *bool {
	if !nb.Valid {
		return nil
	}
	return &nb.Bool
}
func nullableS(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

// Utility to wrap a Dumper with gzip streaming
func GzipStream(ctx context.Context, d Dumper, w Window, put func(r io.Reader) error) (rows int64, bytes int64, err error) {
	pr, pw := io.Pipe()
	gw := gzip.NewWriter(pw)

	type res struct {
		rows int64
		err  error
	}
	ch := make(chan res, 1)

	go func() {
		defer gw.Close()
		rows, derr := d.DumpHour(ctx, w, gw)
		_ = pw.CloseWithError(derr)
		ch <- res{rows, derr}
	}()

	if err = put(pr); err != nil {
		_ = pr.CloseWithError(err)
		<-ch
		return 0, 0, err
	}
	r := <-ch
	return r.rows, 0, r.err // bytes are unknown without a counting writer; add if you wish
}
