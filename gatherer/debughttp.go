package gatherer

import (
	"encoding/json"
	"net/http"
	"time"

	_ "net/http/pprof" // optional pprof endpoints
)

func (g *Gatherer) startDebugHTTP(addr string) {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Queue depths (cheap instantaneous gauges)
	mux.HandleFunc("/queues", func(w http.ResponseWriter, r *http.Request) {
		type Q struct {
			EventQueue    int `json:"event_queue"`
			QuotesQueue   int `json:"quotes_queue"`
			TradesQueue   int `json:"trades_queue"`
			FeaturesQueue int `json:"features_queue"`
		}
		out := Q{
			EventQueue:    len(g.eventChan),
			QuotesQueue:   len(g.quotesCh),
			TradesQueue:   len(g.tradesCh),
			FeaturesQueue: len(g.featuresCh),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		out := struct {
			AssetsCount int      `json:"assets_count"`
			Sample      []string `json:"sample"`
		}{
			AssetsCount: len(g.wsAssetList()),
		}
		// include up to 5 examples to sanity check
		all := g.wsAssetList()
		if len(all) > 5 {
			out.Sample = all[:5]
		} else {
			out.Sample = all
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	// Optional: pprof under /debug/pprof/*
	// The handlers are registered on DefaultServeMux; mount them here.
	mux.Handle("/debug/pprof/", http.DefaultServeMux)

	g.debugSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		g.logger.Info("gatherer debug http listening", "addr", addr)
		if err := g.debugSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			g.logger.Warn("debug http server stopped", "err", err)
		}
	}()
}
