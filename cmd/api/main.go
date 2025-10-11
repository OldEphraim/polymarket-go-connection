package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
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

// context timeout for each request; configurable via env API_TIMEOUT_MS (default 2000ms)
func (s *APIServer) reqCtx(r *http.Request) (context.Context, context.CancelFunc) {
	timeout := 8000
	if ms := os.Getenv("API_TIMEOUT_MS"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 && v <= 10000 {
			timeout = v
		}
	}
	return context.WithTimeout(r.Context(), time.Duration(timeout)*time.Millisecond)
}

// subtle constant-time-ish compare
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

// Middleware to check API key
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

// GET /api/stats
func (s *APIServer) getStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	const q = `
WITH q_lag AS (
  SELECT COALESCE(EXTRACT(EPOCH FROM (now()-MAX(ts)))::bigint, -1) AS lag_sec
  FROM market_quotes
),
t_lag AS (
  SELECT COALESCE(EXTRACT(EPOCH FROM (now()-MAX(ts)))::bigint, -1) AS lag_sec
  FROM market_trades
),
f_lag AS (
  SELECT COALESCE(EXTRACT(EPOCH FROM (now()-MAX(ts)))::bigint, -1) AS lag_sec
  FROM market_features
)
SELECT
  (SELECT COUNT(*) FROM market_scans WHERE is_active = true)                                 AS active_markets,
  (SELECT COUNT(*) FROM market_events WHERE detected_at > now()-interval '24 hours')        AS events_24h,
  (SELECT COUNT(DISTINCT token_id) FROM paper_positions)                                     AS open_positions,
  (SELECT COALESCE(SUM(unrealized_pnl),0) FROM paper_positions)                              AS total_pnl,
  (SELECT COUNT(DISTINCT s.name) FROM trading_sessions ts JOIN strategies s ON ts.strategy_id = s.id) AS strategies_count,
  (SELECT lag_sec FROM q_lag)                                                                AS quotes_lag_sec,
  (SELECT lag_sec FROM t_lag)                                                                AS trades_lag_sec,
  (SELECT lag_sec FROM f_lag)                                                                AS features_lag_sec
;`

	var out struct {
		ActiveMarkets   int64   `json:"active_markets"`
		Events24h       int64   `json:"events_24h"`
		OpenPositions   int64   `json:"open_positions"`
		TotalPnL        float64 `json:"total_pnl"`
		StrategiesCount int64   `json:"strategies_count"`
		QuotesLagSec    int64   `json:"quotes_lag_sec"`
		TradesLagSec    int64   `json:"trades_lag_sec"`
		FeaturesLagSec  int64   `json:"features_lag_sec"`
	}

	row := s.db.QueryRowContext(ctx, q)
	if err := row.Scan(
		&out.ActiveMarkets, &out.Events24h, &out.OpenPositions, &out.TotalPnL, &out.StrategiesCount,
		&out.QuotesLagSec, &out.TradesLagSec, &out.FeaturesLagSec,
	); err != nil {
		s.log.Error("getStats", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}

	writeJSON(w, http.StatusOK, out)
}

// GET /api/market-events  (now returns metadata too; safe interval)
func (s *APIServer) getMarketEvents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	limit, offset := parseLimitOffset(r, 100)
	hours := atoiDefault(r.URL.Query().Get("hours"), 1)
	if hours < 1 || hours > 168 {
		hours = 1
	}

	// We avoid fmt.Sprintf into SQL; build interval via text param → ::interval
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

// GET /api/trades  (paper orders summary stays as-is, but safer + ctx)
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
			// If you later store explicit exit_price, replace this heuristic
			if t.Size > 0 {
				ep := t.EntryPrice + (t.PnL / t.Size)
				t.ExitPrice = &ep
			}
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

// NEW: GET /api/market-trades  (raw trades from ws ingest)
func (s *APIServer) getMarketTrades(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	limit, offset := parseLimitOffset(r, 200)
	token := r.URL.Query().Get("token_id")

	q := `
SELECT token_id, ts, price, size, aggressor
FROM market_trades
WHERE ($1 = '' OR token_id = $1)
ORDER BY ts DESC
LIMIT $2 OFFSET $3;
`
	rows, err := s.db.QueryContext(ctx, q, token, limit, offset)
	if err != nil {
		s.log.Error("getMarketTrades", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	type trade struct {
		TokenID   string    `json:"token_id"`
		TS        time.Time `json:"ts"`
		Price     float64   `json:"price"`
		Size      float64   `json:"size"`
		Aggressor string    `json:"aggressor"` // buy/sell
	}
	out := []trade{}
	for rows.Next() {
		var t trade
		if err := rows.Scan(&t.TokenID, &t.TS, &t.Price, &t.Size, &t.Aggressor); err != nil {
			continue
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

// NEW: GET /api/quotes  (latest quote per token, or for a specific token)
func (s *APIServer) getQuotes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	limit, offset := parseLimitOffset(r, 500)
	token := r.URL.Query().Get("token_id")

	var q string
	var args []any
	if token != "" {
		q = `
SELECT token_id, ts, best_bid, best_ask, (best_bid+best_ask)/2.0 AS mid, spread_bps
FROM market_quotes
WHERE token_id = $1
ORDER BY ts DESC
LIMIT $2 OFFSET $3;`
		args = []any{token, limit, offset}
	} else {
		// DISTINCT ON is ideal for “latest per token”
		q = `
SELECT DISTINCT ON (token_id)
  token_id, ts, best_bid, best_ask,
  (best_bid+best_ask)/2.0 AS mid, spread_bps
FROM market_quotes
ORDER BY token_id, ts DESC
LIMIT $1 OFFSET $2;`
		args = []any{limit, offset}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		s.log.Error("getQuotes", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	type quote struct {
		TokenID  string    `json:"token_id"`
		TS       time.Time `json:"ts"`
		BestBid  float64   `json:"best_bid"`
		BestAsk  float64   `json:"best_ask"`
		Mid      float64   `json:"mid"`
		SpreadBp float64   `json:"spread_bps"`
	}
	var out []quote
	for rows.Next() {
		var q quote
		if err := rows.Scan(&q.TokenID, &q.TS, &q.BestBid, &q.BestAsk, &q.Mid, &q.SpreadBp); err != nil {
			continue
		}
		out = append(out, q)
	}
	writeJSON(w, http.StatusOK, out)
}

// NEW: GET /api/features  (recent features)
func (s *APIServer) getFeatures(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	limit, offset := parseLimitOffset(r, 500)
	mins := atoiDefault(r.URL.Query().Get("minutes"), 5)
	if mins < 1 || mins > 1440 {
		mins = 5
	}
	token := r.URL.Query().Get("token_id")

	const base = `
SELECT token_id, ts, vol_1m, signed_flow_1m, spread_bps, zscore_5m
FROM market_features
WHERE ts > now() - ($1::text || ' minutes')::interval
`
	order := ` ORDER BY ts DESC LIMIT $2 OFFSET $3;`

	var q string
	var args []any
	if token != "" {
		q = base + " AND token_id = $4 " + order
		args = []any{strconv.Itoa(mins), limit, offset, token}
	} else {
		q = base + order
		args = []any{strconv.Itoa(mins), limit, offset}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		s.log.Error("getFeatures", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	type feat struct {
		TokenID      string    `json:"token_id"`
		TS           time.Time `json:"ts"`
		Vol1m        float64   `json:"vol_1m"`
		SignedFlow1m float64   `json:"signed_flow_1m"`
		SpreadBps    float64   `json:"spread_bps"`
		ZScore5m     float64   `json:"zscore_5m"`
	}
	var out []feat
	for rows.Next() {
		var f feat
		if err := rows.Scan(&f.TokenID, &f.TS, &f.Vol1m, &f.SignedFlow1m, &f.SpreadBps, &f.ZScore5m); err != nil {
			continue
		}
		out = append(out, f)
	}
	writeJSON(w, http.StatusOK, out)
}

// NEW: GET /api/snapshot/{token_id}  (everything a card needs)
func (s *APIServer) getSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token := mux.Vars(r)["token_id"]
	if token == "" {
		writeErr(w, http.StatusBadRequest, "missing token_id")
		return
	}

	type (
		quote struct {
			TS       time.Time `json:"ts"`
			BestBid  float64   `json:"best_bid"`
			BestAsk  float64   `json:"best_ask"`
			Mid      float64   `json:"mid"`
			SpreadBp float64   `json:"spread_bps"`
		}
		trade struct {
			TS        time.Time `json:"ts"`
			Price     float64   `json:"price"`
			Size      float64   `json:"size"`
			Aggressor string    `json:"aggressor"`
		}
		feat struct {
			TS           time.Time `json:"ts"`
			Vol1m        float64   `json:"vol_1m"`
			SignedFlow1m float64   `json:"signed_flow_1m"`
			SpreadBps    float64   `json:"spread_bps"`
			ZScore5m     float64   `json:"zscore_5m"`
		}
		ev struct {
			EventType  string          `json:"event_type"`
			DetectedAt time.Time       `json:"detected_at"`
			OldValue   *float64        `json:"old_value,omitempty"`
			NewValue   *float64        `json:"new_value,omitempty"`
			Metadata   map[string]any  `json:"metadata,omitempty"`
			RawMeta    json.RawMessage `json:"-"`
		}
	)

	var (
		q  *quote
		t  *trade
		f  *feat
		es []ev
	)

	// latest quote
	const q1 = `
SELECT ts, best_bid, best_ask, (best_bid+best_ask)/2.0 AS mid, spread_bps
FROM market_quotes WHERE token_id = $1
ORDER BY ts DESC LIMIT 1;`
	// latest trade
	const q2 = `
SELECT ts, price, size, aggressor
FROM market_trades WHERE token_id = $1
ORDER BY ts DESC LIMIT 1;`
	// latest features
	const q3 = `
SELECT ts, vol_1m, signed_flow_1m, spread_bps, zscore_5m
FROM market_features WHERE token_id = $1
ORDER BY ts DESC LIMIT 1;`
	// recent events (+metadata)
	const q4 = `
SELECT event_type, detected_at, old_value, new_value, metadata
FROM market_events
WHERE token_id = $1
ORDER BY detected_at DESC
LIMIT 20;`

	// Run sequentially (simple + predictable)
	{
		row := s.db.QueryRowContext(ctx, q1, token)
		var qq quote
		if err := row.Scan(&qq.TS, &qq.BestBid, &qq.BestAsk, &qq.Mid, &qq.SpreadBp); err == nil {
			q = &qq
		}
	}
	{
		row := s.db.QueryRowContext(ctx, q2, token)
		var tt trade
		if err := row.Scan(&tt.TS, &tt.Price, &tt.Size, &tt.Aggressor); err == nil {
			t = &tt
		}
	}
	{
		row := s.db.QueryRowContext(ctx, q3, token)
		var ff feat
		if err := row.Scan(&ff.TS, &ff.Vol1m, &ff.SignedFlow1m, &ff.SpreadBps, &ff.ZScore5m); err == nil {
			f = &ff
		}
	}
	{
		rows, err := s.db.QueryContext(ctx, q4, token)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var e ev
				var oldV, newV sql.NullString
				if err := rows.Scan(&e.EventType, &e.DetectedAt, &oldV, &newV, &e.RawMeta); err != nil {
					continue
				}
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
				if len(e.RawMeta) > 0 {
					_ = json.Unmarshal(e.RawMeta, &e.Metadata)
				}
				es = append(es, e)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token_id": token,
		"quote":    q,
		"trade":    t,
		"features": f,
		"events":   es,
	})
}

// GET /api/strategy-performance (kept, but with ctx + sturdy errors)
func (s *APIServer) getStrategyPerformance(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	const q = `
SELECT 
  s.name AS strategy,
  COUNT(DISTINCT p.token_id) AS positions,
  COALESCE(SUM(p.unrealized_pnl), 0) AS unrealized_pnl
FROM strategies s
LEFT JOIN trading_sessions ts ON s.id = ts.strategy_id
LEFT JOIN paper_positions p    ON ts.id = p.session_id
GROUP BY s.name
ORDER BY s.name;
`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		s.log.Error("getStrategyPerformance", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	type perf struct {
		Strategy      string  `json:"strategy"`
		Positions     int64   `json:"positions"`
		UnrealizedPnL float64 `json:"unrealized_pnl"`
	}
	var out []perf
	for rows.Next() {
		var p perf
		if err := rows.Scan(&p.Strategy, &p.Positions, &p.UnrealizedPnL); err != nil {
			continue
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
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

	// SQLC store (if you later want typed methods) + raw DB for custom SQL
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
	r.HandleFunc("/api/strategy-performance", server.authenticate(server.getStrategyPerformance)).Methods("GET")

	// New endpoints for the frontend
	r.HandleFunc("/api/market-trades", server.authenticate(server.getMarketTrades)).Methods("GET")
	r.HandleFunc("/api/quotes", server.authenticate(server.getQuotes)).Methods("GET")
	r.HandleFunc("/api/features", server.authenticate(server.getFeatures)).Methods("GET")
	r.HandleFunc("/api/snapshot/{token_id}", server.authenticate(server.getSnapshot)).Methods("GET")

	// CORS + logging + recover
	cors := handlers.CORS(
		handlers.AllowedOrigins([]string{"*"}), // tighten for prod
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

func ensureIndexes(ctx context.Context, db *sql.DB, log *slog.Logger) {
	stmts := []string{
		"SET statement_timeout = '30s'",

		// “last row” helpers for O(1) lag checks, feeds, etc.
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_quotes_ts              ON market_quotes (ts DESC)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_trades_ts              ON market_trades (ts DESC)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_features_ts            ON market_features (ts DESC)`,

		// /api/stats 24h events
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_events_detected        ON market_events (detected_at DESC)`,

		// common API lookups
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_quotes_token_ts        ON market_quotes (token_id, ts DESC)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_trades_token_ts        ON market_trades (token_id, ts DESC)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_events_token_detected  ON market_events (token_id, detected_at DESC)`,
	}

	for _, s := range stmts {
		if strings.HasPrefix(s, "SET ") {
			if _, err := db.ExecContext(ctx, s); err != nil {
				log.Warn("index setup: statement_timeout failed", "err", err)
			}
			continue
		}
		if _, err := db.ExecContext(ctx, s); err != nil {
			// CREATE INDEX CONCURRENTLY can fail if something else is holding locks.
			// Log and keep going — it’s safe to retry on next boot.
			log.Warn("index setup: create failed", "sql", s, "err", err)
		} else {
			log.Info("index ensured", "sql", s)
		}
	}
}
