package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type APIServer struct {
	store  *db.Store
	db     *sql.DB
	apiKey string
	log    *slog.Logger
}

// --------- helpers ---------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return i
}

func parseLimitOffset(r *http.Request, defLimit int) (limit, offset int) {
	limit = atoiDefault(r.URL.Query().Get("limit"), defLimit)
	if limit < 1 || limit > 1000 {
		limit = defLimit
	}
	offset = atoiDefault(r.URL.Query().Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	return
}

func (s *APIServer) reqCtx(r *http.Request) (context.Context, context.CancelFunc) {
	timeout := 8000
	if ms := os.Getenv("API_TIMEOUT_MS"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 && v <= 10000 {
			timeout = v
		}
	}
	return context.WithTimeout(r.Context(), time.Duration(timeout)*time.Millisecond)
}

func safeKeyEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (s *APIServer) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if !safeKeyEq(key, s.apiKey) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	}
}

// ---------- endpoints ----------

func (s *APIServer) getStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	const q = `
WITH
  active_markets AS (
    SELECT COUNT(*) AS n FROM market_scans WHERE is_active = TRUE
  ),
  events_24h AS (
    SELECT COUNT(*) AS n FROM market_events WHERE detected_at > NOW() - INTERVAL '24 hours'
  ),
  positions AS (
    SELECT
      COALESCE(COUNT(*),0) AS open_positions,
      COALESCE(SUM(unrealized_pnl),0) AS total_pnl
    FROM paper_positions
  ),
  strategies_active AS (
    SELECT COUNT(DISTINCT po.session_id) AS n
    FROM paper_orders po
    WHERE po.created_at > NOW() - INTERVAL '24 hours'
  ),
  quotes_lag AS (
    SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MAX(ts)))::int, 0) AS sec FROM market_quotes
  ),
  trades_lag AS (
    SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MAX(ts)))::int, 0) AS sec FROM market_trades
  ),
  features_lag AS (
    SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MAX(ts)))::int, 0) AS sec FROM market_features
  ),
  db_size AS (
    SELECT pg_size_pretty(pg_database_size(current_database())) AS size
  ),
  total_rows AS (
    SELECT COUNT(*) AS n FROM market_features
  ),
  features_per_min AS (
    SELECT COUNT(*) AS n FROM market_features WHERE ts > NOW() - INTERVAL '1 minute'
  )
SELECT
  (SELECT n FROM active_markets) AS active_markets,
  (SELECT n FROM events_24h) AS events_24h,
  (SELECT open_positions FROM positions) AS open_positions,
  (SELECT total_pnl FROM positions) AS total_pnl,
  (SELECT n FROM strategies_active) AS strategies_count,
  (SELECT sec FROM quotes_lag) AS quotes_lag_sec,
  (SELECT sec FROM trades_lag) AS trades_lag_sec,
  (SELECT sec FROM features_lag) AS features_lag_sec,
  (SELECT size FROM db_size) AS db_size,
  (SELECT n FROM total_rows) AS total_rows,
  (SELECT n FROM features_per_min) AS features_per_minute
;`

	var out struct {
		ActiveMarkets  int64   `json:"active_markets"`
		Events24h      int64   `json:"events_24h"`
		OpenPositions  int64   `json:"open_positions"`
		TotalPnL       float64 `json:"total_pnl"`
		Strategies     int64   `json:"strategies_count"`
		QuotesLagSec   int64   `json:"quotes_lag_sec"`
		TradesLagSec   int64   `json:"trades_lag_sec"`
		FeaturesLagSec int64   `json:"features_lag_sec"`
		DBSize         string  `json:"db_size"`
		TotalRows      int64   `json:"total_rows"`
		FeaturesPerMin int64   `json:"features_per_minute"`
	}

	row := s.db.QueryRowContext(ctx, q)
	if err := row.Scan(
		&out.ActiveMarkets,
		&out.Events24h,
		&out.OpenPositions,
		&out.TotalPnL,
		&out.Strategies,
		&out.QuotesLagSec,
		&out.TradesLagSec,
		&out.FeaturesLagSec,
		&out.DBSize,
		&out.TotalRows,
		&out.FeaturesPerMin,
	); err != nil {
		slog.Error("getStats", "err", err)
		http.Error(w, "stats error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *APIServer) getMarketEvents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	limit, offset := parseLimitOffset(r, 100)
	hours := atoiDefault(r.URL.Query().Get("hours"), 1)
	if hours < 1 || hours > 168 {
		hours = 1
	}

	const q = `
SELECT
  me.token_id,
  me.event_type,
  me.old_value,
  me.new_value,
  me.detected_at,
  ms.question,
  me.metadata
FROM market_events me
LEFT JOIN market_scans ms ON me.token_id = ms.token_id
WHERE me.detected_at > now() - ($3::text || ' hours')::interval
ORDER BY me.detected_at DESC
LIMIT $1 OFFSET $2;
`
	rows, err := s.db.QueryContext(ctx, q, limit, offset, strconv.Itoa(hours))
	if err != nil {
		s.log.Error("getMarketEvents", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	type ev struct {
		TokenID    string          `json:"token_id"`
		EventType  string          `json:"event_type"`
		OldValue   *float64        `json:"old_value,omitempty"`
		NewValue   *float64        `json:"new_value,omitempty"`
		DetectedAt time.Time       `json:"detected_at"`
		Question   string          `json:"question"`
		Metadata   map[string]any  `json:"metadata,omitempty"`
		RawMeta    json.RawMessage `json:"-"`
	}
	out := make([]ev, 0, limit)

	for rows.Next() {
		var e ev
		var evType sql.NullString
		var oldV, newV sql.NullString
		var qn sql.NullString
		if err := rows.Scan(&e.TokenID, &evType, &oldV, &newV, &e.DetectedAt, &qn, &e.RawMeta); err != nil {
			continue
		}
		e.EventType = evType.String
		if oldV.Valid {
			if v, err := strconv.ParseFloat(oldV.String, 64); err == nil {
				e.OldValue = &v
			}
		}
		if newV.Valid {
			if v, err := strconv.ParseFloat(newV.String, 64); err == nil {
				e.NewValue = &v
			}
		}
		if qn.Valid {
			e.Question = qn.String
		}
		if len(e.RawMeta) > 0 {
			_ = json.Unmarshal(e.RawMeta, &e.Metadata)
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *APIServer) getTrades(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)

	const q = `
SELECT 
  po.id,
  po.token_id,
  COALESCE(s.name,'unknown') AS strategy,
  po.side,
  po.price AS entry_price,
  po.size,
  po.status,
  po.created_at AS entry_time,
  po.filled_at  AS exit_time,
  COALESCE(ms.question, po.token_id) AS market_id,
  COALESCE(pp.unrealized_pnl, 0) AS pnl
FROM paper_orders po
LEFT JOIN trading_sessions ts ON po.session_id = ts.id
LEFT JOIN strategies s ON ts.strategy_id = s.id
LEFT JOIN market_scans ms ON po.token_id = ms.token_id
LEFT JOIN paper_positions pp ON pp.session_id = po.session_id AND pp.token_id = po.token_id
ORDER BY po.created_at DESC
LIMIT $1;
`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		s.log.Error("getTrades", "err", err)
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	defer rows.Close()

	type trade struct {
		ID         int        `json:"id"`
		TokenID    string     `json:"token_id"`
		Strategy   string     `json:"strategy"`
		MarketID   string     `json:"market_id"`
		Side       string     `json:"side"`
		EntryPrice float64    `json:"entry_price"`
		Size       float64    `json:"size"`
		Status     string     `json:"status"`
		EntryTime  time.Time  `json:"entry_time"`
		ExitTime   *time.Time `json:"exit_time,omitempty"`
		ExitPrice  *float64   `json:"exit_price,omitempty"`
		PnL        float64    `json:"pnl"`
	}

	var out []trade
	for rows.Next() {
		var t trade
		var marketID sql.NullString
		var exitTime sql.NullTime
		if err := rows.Scan(&t.ID, &t.TokenID, &t.Strategy, &t.Side, &t.EntryPrice, &t.Size, &t.Status, &t.EntryTime, &exitTime, &marketID, &t.PnL); err != nil {
			continue
		}
		if marketID.Valid {
			t.MarketID = marketID.String
		} else {
			t.MarketID = t.TokenID
		}
		if exitTime.Valid {
			t.ExitTime = &exitTime.Time
			if t.Size > 0 {
				ep := t.EntryPrice + (t.PnL / t.Size)
				t.ExitPrice = &ep
			}
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

// Strategy-specific endpoints
func (s *APIServer) getStrategyPerformanceDetail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	vars := mux.Vars(r)
	strategy := vars["name"]

	// Get overall metrics
	var metrics struct {
		TotalPnL    float64 `json:"total_pnl"`
		Wins        int     `json:"wins"`
		Losses      int     `json:"losses"`
		TotalTrades int     `json:"total_trades"`
		AvgWin      float64 `json:"avg_win"`
		AvgLoss     float64 `json:"avg_loss"`
	}

	err := s.db.QueryRowContext(ctx, `
		SELECT 
			COALESCE(SUM(pnl), 0) as total_pnl,
			COUNT(CASE WHEN pnl > 0 THEN 1 END) as wins,
			COUNT(CASE WHEN pnl < 0 THEN 1 END) as losses,
			COUNT(*) as total_trades,
			COALESCE(AVG(CASE WHEN pnl > 0 THEN pnl END), 0) as avg_win,
			COALESCE(AVG(CASE WHEN pnl < 0 THEN pnl END), 0) as avg_loss
		FROM paper_positions
		WHERE strategy = $1 AND status = 'closed'
	`, strategy).Scan(&metrics.TotalPnL, &metrics.Wins, &metrics.Losses,
		&metrics.TotalTrades, &metrics.AvgWin, &metrics.AvgLoss)

	if err != nil {
		s.log.Error("getStrategyPerformanceDetail", "err", err)
	}

	// Get daily P&L for chart
	rows, err := s.db.QueryContext(ctx, `
		SELECT 
			DATE(exited_at) as date,
			SUM(pnl) as daily_pnl,
			SUM(SUM(pnl)) OVER (ORDER BY DATE(exited_at)) as cumulative_pnl
		FROM paper_positions
		WHERE strategy = $1 AND status = 'closed' AND exited_at > NOW() - INTERVAL '30 days'
		GROUP BY DATE(exited_at)
		ORDER BY date
	`, strategy)

	var dailyPnL []map[string]interface{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var date time.Time
			var daily, cumulative float64
			if err := rows.Scan(&date, &daily, &cumulative); err == nil {
				dailyPnL = append(dailyPnL, map[string]interface{}{
					"date":           date.Format("Jan 2"),
					"daily_pnl":      daily,
					"cumulative_pnl": cumulative,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_pnl":    metrics.TotalPnL,
		"wins":         metrics.Wins,
		"losses":       metrics.Losses,
		"total_trades": metrics.TotalTrades,
		"avg_win":      metrics.AvgWin,
		"avg_loss":     metrics.AvgLoss,
		"daily_pnl":    dailyPnL,
	})
}

func (s *APIServer) getStrategyPositions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	vars := mux.Vars(r)
	strategy := vars["name"]

	rows, err := s.db.QueryContext(ctx, `
		SELECT 
			p.token_id,
			COALESCE(m.question, p.market, 'Unknown') as market,
			p.side,
			p.size,
			p.entry_price,
			COALESCE(
				(SELECT (best_bid + best_ask) / 2.0 FROM market_quotes WHERE token_id = p.token_id ORDER BY ts DESC LIMIT 1),
				p.entry_price
			) as current_price,
			p.entered_at,
			EXTRACT(EPOCH FROM (NOW() - p.entered_at))/3600 as hours_held
		FROM paper_positions p
		LEFT JOIN markets m ON m.token_id = p.token_id
		WHERE p.strategy = $1 AND p.status = 'open'
		ORDER BY p.entered_at DESC
	`, strategy)

	if err != nil {
		s.log.Error("getStrategyPositions", "err", err)
		writeJSON(w, http.StatusOK, []map[string]interface{}{})
		return
	}
	defer rows.Close()

	var positions []map[string]interface{}
	for rows.Next() {
		var tokenID, market, side string
		var size, entryPrice, currentPrice, hoursHeld float64
		var enteredAt time.Time

		if err := rows.Scan(&tokenID, &market, &side, &size, &entryPrice, &currentPrice, &enteredAt, &hoursHeld); err != nil {
			continue
		}

		unrealizedPnL := (currentPrice - entryPrice) * size
		if side == "NO" {
			unrealizedPnL = (entryPrice - currentPrice) * size
		}

		duration := fmt.Sprintf("%.1fh", hoursHeld)
		if hoursHeld < 1 {
			duration = fmt.Sprintf("%dm", int(hoursHeld*60))
		}

		positions = append(positions, map[string]interface{}{
			"token_id":       tokenID,
			"market":         market,
			"side":           side,
			"size":           size,
			"entry_price":    entryPrice,
			"current_price":  currentPrice,
			"unrealized_pnl": unrealizedPnL,
			"duration":       duration,
		})
	}

	writeJSON(w, http.StatusOK, positions)
}

func (s *APIServer) getStrategyTrades(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	vars := mux.Vars(r)
	strategy := vars["name"]
	limit := atoiDefault(r.URL.Query().Get("limit"), 20)

	rows, err := s.db.QueryContext(ctx, `
		SELECT 
			p.token_id,
			COALESCE(m.question, p.market, 'Unknown') as market,
			p.side,
			p.entry_price,
			p.exit_price,
			p.pnl,
			p.exit_reason,
			p.exited_at
		FROM paper_positions p
		LEFT JOIN markets m ON m.token_id = p.token_id
		WHERE p.strategy = $1 AND p.status = 'closed'
		ORDER BY p.exited_at DESC
		LIMIT $2
	`, strategy, limit)

	if err != nil {
		s.log.Error("getStrategyTrades", "err", err)
		writeJSON(w, http.StatusOK, []map[string]interface{}{})
		return
	}
	defer rows.Close()

	var trades []map[string]interface{}
	for rows.Next() {
		var tokenID, market, side, exitReason string
		var entryPrice, exitPrice, pnl float64
		var exitedAt time.Time

		if err := rows.Scan(&tokenID, &market, &side, &entryPrice, &exitPrice, &pnl, &exitReason, &exitedAt); err != nil {
			continue
		}

		trades = append(trades, map[string]interface{}{
			"token_id":    tokenID,
			"market":      market,
			"side":        side,
			"entry_price": entryPrice,
			"exit_price":  exitPrice,
			"pnl":         pnl,
			"exit_reason": exitReason,
			"exited_at":   exitedAt,
		})
	}

	writeJSON(w, http.StatusOK, trades)
}

// --------- main ---------

func main() {
	_ = godotenv.Load()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		log.Fatal("API_KEY must be set")
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL must be set")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	store, err := db.NewStore(dbURL)
	if err != nil {
		log.Fatal("db.NewStore:", err)
	}
	rawDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("sql.Open:", err)
	}
	rawDB.SetMaxOpenConns(25)
	rawDB.SetMaxIdleConns(25)
	rawDB.SetConnMaxIdleTime(2 * time.Minute)
	defer rawDB.Close()

	server := &APIServer{
		store:  store,
		db:     rawDB,
		apiKey: apiKey,
		log:    logger,
	}

	r := mux.NewRouter()

	// Public
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}).Methods("GET")

	// Protected
	r.HandleFunc("/api/stats", server.authenticate(server.getStats)).Methods("GET")
	r.HandleFunc("/api/trades", server.authenticate(server.getTrades)).Methods("GET")
	r.HandleFunc("/api/market-events", server.authenticate(server.getMarketEvents)).Methods("GET")

	// Strategy-specific endpoints
	r.HandleFunc("/api/strategies/{name}/performance", server.authenticate(server.getStrategyPerformanceDetail)).Methods("GET")
	r.HandleFunc("/api/strategies/{name}/positions", server.authenticate(server.getStrategyPositions)).Methods("GET")
	r.HandleFunc("/api/strategies/{name}/trades", server.authenticate(server.getStrategyTrades)).Methods("GET")

	// CORS + logging + recover
	cors := handlers.CORS(
		handlers.AllowedOrigins([]string{"*"}),
		handlers.AllowedMethods([]string{"GET", "OPTIONS"}),
		handlers.AllowedHeaders([]string{"Content-Type", "X-API-Key"}),
	)
	h := handlers.RecoveryHandler()(handlers.LoggingHandler(os.Stdout, cors(r)))

	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Info("api starting", "port", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("api stopped", "err", err)
	}
}
