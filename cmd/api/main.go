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

func (s *APIServer) reqCtx(r *http.Request) (context.Context, context.CancelFunc) {
	timeout := 8000
	if ms := os.Getenv("API_TIMEOUT_MS"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 && v <= 20000 {
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

// /api/stats — high-level system & trading stats
func (s *APIServer) getStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	// total_pnl = realized (sessions) + sum unrealized (open positions)
	const q = `
WITH
  active_markets AS (
    SELECT COUNT(*) AS n FROM market_scans WHERE is_active = TRUE
  ),
  events_24h AS (
    SELECT COUNT(*) AS n FROM market_events WHERE detected_at > NOW() - INTERVAL '24 hours'
  ),
  open_positions AS (
    SELECT COUNT(*) AS n, COALESCE(SUM(unrealized_pnl),0) AS unreal
    FROM paper_positions WHERE shares > 0
  ),
  realized AS (
    SELECT COALESCE(SUM(realized_pnl),0) AS r
    FROM trading_sessions
  ),
  strategies_active AS (
    SELECT COUNT(DISTINCT ts.id) AS n
    FROM paper_trades t
    JOIN trading_sessions ts ON ts.id = t.session_id
    WHERE t.created_at > NOW() - INTERVAL '24 hours'
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
  features_per_min AS (
    SELECT COUNT(*) AS n FROM market_features WHERE ts > NOW() - INTERVAL '1 minute'
  ),
  quotes_5m AS (
    SELECT COALESCE(COUNT(*),0)::bigint AS n FROM market_quotes WHERE ts > NOW() - INTERVAL '5 minutes'
  ),
  trades_5m AS (
    SELECT COALESCE(COUNT(*),0)::bigint AS n FROM market_trades WHERE ts > NOW() - INTERVAL '5 minutes'
  )
SELECT
  (SELECT n FROM active_markets)                                        AS active_markets,
  (SELECT n FROM events_24h)                                            AS events_24h,
  (SELECT n FROM open_positions)                                        AS open_positions,
  (SELECT r FROM realized) + (SELECT unreal FROM open_positions)        AS total_pnl,
  (SELECT n FROM strategies_active)                                     AS strategies_count,
  (SELECT sec FROM quotes_lag)                                          AS quotes_lag_sec,
  (SELECT sec FROM trades_lag)                                          AS trades_lag_sec,
  (SELECT sec FROM features_lag)                                        AS features_lag_sec,
  (SELECT size FROM db_size)                                            AS db_size,
  (SELECT n FROM features_per_min)                                      AS features_per_minute,
  (SELECT (n / 5.0) FROM quotes_5m)                                     AS ingest_quotes_per_min,
  (SELECT (n / 5.0) FROM trades_5m)                                     AS ingest_trades_per_min
;`

	var out struct {
		ActiveMarkets      int64            `json:"active_markets"`
		Events24h          int64            `json:"events_24h"`
		OpenPositions      int64            `json:"open_positions"`
		TotalPnL           float64          `json:"total_pnl"`
		Strategies         int64            `json:"strategies_count"`
		QuotesLagSec       int64            `json:"quotes_lag_sec"`
		TradesLagSec       int64            `json:"trades_lag_sec"`
		FeaturesLagSec     int64            `json:"features_lag_sec"`
		DBSize             string           `json:"db_size"`
		FeaturesPerMin     int64            `json:"features_per_minute"`
		IngestQuotesPerMin float64          `json:"ingest_quotes_per_min"`
		IngestTradesPerMin float64          `json:"ingest_trades_per_min"`
		WriterQueueDepths  map[string]int64 `json:"writer_queue_depths,omitempty"`
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
		&out.FeaturesPerMin,
		&out.IngestQuotesPerMin,
		&out.IngestTradesPerMin,
	); err != nil {
		s.log.Error("getStats", "err", err)
		http.Error(w, "stats error", http.StatusInternalServerError)
		return
	}

	// Optional: fetch writer queue depths from the gatherer’s internal debug endpoint.
	// Set GATHERER_DEBUG_URL (e.g., http://gatherer:6060). If unset or fails, we omit the field.
	if base := os.Getenv("GATHERER_DEBUG_URL"); base != "" {
		reqCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(base, "/")+"/debug/queues", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp != nil && resp.Body != nil {
			defer resp.Body.Close()
			var qd map[string]int64
			if json.NewDecoder(resp.Body).Decode(&qd) == nil {
				out.WriterQueueDepths = qd
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// /api/market-events — live event feed
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
  COALESCE(ms.question, me.token_id) AS question,
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
		TokenID    string                 `json:"token_id"`
		EventType  string                 `json:"event_type"`
		OldValue   *float64               `json:"old_value,omitempty"`
		NewValue   *float64               `json:"new_value,omitempty"`
		DetectedAt time.Time              `json:"detected_at"`
		Question   string                 `json:"question"`
		Metadata   map[string]interface{} `json:"metadata,omitempty"`
	}
	out := make([]ev, 0, limit)

	for rows.Next() {
		var e ev
		var evType sql.NullString
		var oldV, newV sql.NullFloat64
		var qn sql.NullString
		var raw json.RawMessage
		if err := rows.Scan(&e.TokenID, &evType, &oldV, &newV, &e.DetectedAt, &qn, &raw); err != nil {
			continue
		}
		e.EventType = evType.String
		if oldV.Valid {
			val := oldV.Float64
			e.OldValue = &val
		}
		if newV.Valid {
			val := newV.Float64
			e.NewValue = &val
		}
		if qn.Valid {
			e.Question = qn.String
		} else {
			e.Question = e.TokenID
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Metadata)
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

// /api/fills — recent paper fills across all strategies (from paper_trades)
func (s *APIServer) getFills(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)

	const q = `
SELECT
  t.id,
  t.token_id,
  COALESCE(s.name,'unknown') AS strategy,
  t.side,
  t.price,
  t.shares,
  t.notional,
  t.realized_pnl,
  t.created_at,
  COALESCE(ms.question, t.token_id) AS market
FROM paper_trades t
JOIN trading_sessions ts ON ts.id = t.session_id
LEFT JOIN strategies s ON s.id = ts.strategy_id
LEFT JOIN market_scans ms ON ms.token_id = t.token_id
ORDER BY t.created_at DESC
LIMIT $1;
`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		s.log.Error("getFills", "err", err)
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	defer rows.Close()

	type fill struct {
		ID          int64     `json:"id"`
		TokenID     string    `json:"token_id"`
		Market      string    `json:"market"`
		Strategy    string    `json:"strategy"`
		Side        string    `json:"side"`
		Price       float64   `json:"price"`
		Shares      float64   `json:"shares"`
		Notional    float64   `json:"notional"`
		RealizedPnL float64   `json:"realized_pnl"`
		CreatedAt   time.Time `json:"created_at"`
	}
	var out []fill
	for rows.Next() {
		var f fill
		if err := rows.Scan(&f.ID, &f.TokenID, &f.Strategy, &f.Side, &f.Price, &f.Shares, &f.Notional, &f.RealizedPnL, &f.CreatedAt, &f.Market); err != nil {
			continue
		}
		out = append(out, f)
	}
	writeJSON(w, http.StatusOK, out)
}

// /api/strategies — per-strategy summary (for cards/leaderboard)
func (s *APIServer) listStrategies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	const q = `
WITH
  sess AS (
    SELECT ts.id, ts.strategy_id, ts.current_balance, ts.realized_pnl
    FROM trading_sessions ts
  ),
  unreal AS (
    SELECT ts.strategy_id, COALESCE(SUM(pp.unrealized_pnl),0) AS unreal
    FROM paper_positions pp
    JOIN trading_sessions ts ON ts.id = pp.session_id
    WHERE pp.shares > 0
    GROUP BY ts.strategy_id
  ),
  recent AS (
    SELECT ts.strategy_id, COUNT(*) AS fills_24h, MAX(t.created_at) AS last_trade_at
    FROM paper_trades t
    JOIN trading_sessions ts ON ts.id = t.session_id
    WHERE t.created_at > NOW() - INTERVAL '24 hours'
    GROUP BY ts.strategy_id
  )
SELECT
  s.name,
  COALESCE(SUM(sess.realized_pnl),0) AS realized_pnl,
  COALESCE(u.unreal,0) AS unrealized_pnl,
  COALESCE(r.fills_24h,0) AS fills_24h,
  r.last_trade_at
FROM strategies s
LEFT JOIN sess ON sess.strategy_id = s.id
LEFT JOIN unreal u ON u.strategy_id = s.id
LEFT JOIN recent r ON r.strategy_id = s.id
GROUP BY s.name, u.unreal, r.fills_24h, r.last_trade_at
ORDER BY (COALESCE(SUM(sess.realized_pnl),0) + COALESCE(u.unreal,0)) DESC, s.name;
`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		s.log.Error("listStrategies", "err", err)
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	defer rows.Close()

	type row struct {
		Name          string     `json:"name"`
		RealizedPnL   float64    `json:"realized_pnl"`
		UnrealizedPnL float64    `json:"unrealized_pnl"`
		Fills24h      int64      `json:"fills_24h"`
		LastTradeAt   *time.Time `json:"last_trade_at,omitempty"`
		TotalPnL      float64    `json:"total_pnl"`
	}
	var out []row
	for rows.Next() {
		var r row
		var lta sql.NullTime
		if err := rows.Scan(&r.Name, &r.RealizedPnL, &r.UnrealizedPnL, &r.Fills24h, &lta); err != nil {
			continue
		}
		if lta.Valid {
			r.LastTradeAt = &lta.Time
		}
		r.TotalPnL = r.RealizedPnL + r.UnrealizedPnL
		out = append(out, r)
	}
	writeJSON(w, http.StatusOK, out)
}

// /api/strategies/{name}/positions — open positions for a strategy (with correct unrealized P&L)
func (s *APIServer) getStrategyPositions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	strategy := mux.Vars(r)["name"]

	// Derive "side" from the most recent non-EXIT trade per (session, token).
	// Compute current_price from latest mid; fall back to stored current_price/avg_entry_price.
	const q = `
WITH strat AS (
  SELECT id FROM strategies WHERE name = $1
),
sess AS (
  SELECT id FROM trading_sessions WHERE strategy_id IN (SELECT id FROM strat)
),
latest_side AS (
  SELECT t.session_id, t.token_id,
         FIRST_VALUE(t.side) OVER (PARTITION BY t.session_id, t.token_id ORDER BY t.created_at DESC) AS side
  FROM paper_trades t
  WHERE t.side IN ('YES','NO') AND t.session_id IN (SELECT id FROM sess)
),
pos AS (
  SELECT pp.session_id, pp.token_id, pp.shares, pp.avg_entry_price,
         COALESCE(
           (SELECT (mq.best_bid + mq.best_ask)/2.0 FROM market_quotes mq WHERE mq.token_id = pp.token_id ORDER BY mq.ts DESC LIMIT 1),
           pp.current_price,
           pp.avg_entry_price
         ) AS current_price,
         COALESCE(ms.question, pp.token_id) AS market,
         ls.side,
         pp.updated_at
  FROM paper_positions pp
  LEFT JOIN latest_side ls ON ls.session_id = pp.session_id AND ls.token_id = pp.token_id
  LEFT JOIN market_scans ms ON ms.token_id = pp.token_id
  WHERE pp.shares > 0 AND pp.session_id IN (SELECT id FROM sess)
)
SELECT
  token_id,
  market,
  COALESCE(side,'YES') AS side, -- default optimistic if unknown
  shares,
  avg_entry_price,
  current_price,
  CASE WHEN COALESCE(side,'YES') = 'YES'
       THEN (current_price - avg_entry_price) * shares
       ELSE (avg_entry_price - current_price) * shares
  END AS unrealized_pnl,
  updated_at
FROM pos
ORDER BY updated_at DESC, market, token_id;
`
	rows, err := s.db.QueryContext(ctx, q, strategy)
	if err != nil {
		s.log.Error("getStrategyPositions", "err", err)
		writeJSON(w, http.StatusOK, []map[string]interface{}{})
		return
	}
	defer rows.Close()

	var out []map[string]interface{}
	for rows.Next() {
		var tokenID, market, side string
		var shares, entry, current, upnl float64
		var updatedAt time.Time
		if err := rows.Scan(&tokenID, &market, &side, &shares, &entry, &current, &upnl, &updatedAt); err != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"token_id":       tokenID,
			"market":         market,
			"side":           side,
			"shares":         shares,
			"avg_entry":      entry,
			"current_price":  current,
			"unrealized_pnl": upnl,
			"updated_at":     updatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// /api/strategies/{name}/fills — recent fills for a single strategy
func (s *APIServer) getStrategyFills(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	strategy := mux.Vars(r)["name"]
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)

	const q = `
SELECT
  t.id,
  t.token_id,
  t.side,
  t.price,
  t.shares,
  t.notional,
  t.realized_pnl,
  t.created_at,
  COALESCE(ms.question, t.token_id) AS market
FROM paper_trades t
JOIN trading_sessions ts ON ts.id = t.session_id
JOIN strategies s ON s.id = ts.strategy_id
LEFT JOIN market_scans ms ON ms.token_id = t.token_id
WHERE s.name = $1
ORDER BY t.created_at DESC
LIMIT $2;
`
	rows, err := s.db.QueryContext(ctx, q, strategy, limit)
	if err != nil {
		s.log.Error("getStrategyFills", "err", err)
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	defer rows.Close()

	type fill struct {
		ID          int64     `json:"id"`
		TokenID     string    `json:"token_id"`
		Market      string    `json:"market"`
		Side        string    `json:"side"`
		Price       float64   `json:"price"`
		Shares      float64   `json:"shares"`
		Notional    float64   `json:"notional"`
		RealizedPnL float64   `json:"realized_pnl"`
		CreatedAt   time.Time `json:"created_at"`
	}
	var out []fill
	for rows.Next() {
		var f fill
		if err := rows.Scan(&f.ID, &f.TokenID, &f.Side, &f.Price, &f.Shares, &f.Notional, &f.RealizedPnL, &f.CreatedAt, &f.Market); err != nil {
			continue
		}
		out = append(out, f)
	}
	writeJSON(w, http.StatusOK, out)
}

// /api/strategies/{name}/pnl — daily realized/unrealized + cumulative (default 30 days)
func (s *APIServer) getStrategyPnL(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	strategy := mux.Vars(r)["name"]
	days := atoiDefault(r.URL.Query().Get("days"), 30)
	if days < 1 || days > 365 {
		days = 30
	}

	const q = `
WITH strat AS (
  SELECT id FROM strategies WHERE name = $1
),
sess AS (
  SELECT id FROM trading_sessions WHERE strategy_id IN (SELECT id FROM strat)
),
realized AS (
  SELECT DATE(t.created_at) AS d, COALESCE(SUM(t.realized_pnl),0) AS r
  FROM paper_trades t
  WHERE t.session_id IN (SELECT id FROM sess)
    AND t.created_at > NOW() - ($2::text || ' days')::interval
  GROUP BY 1
),
unreal AS (
  -- snapshot unrealized by day using latest price per day
  SELECT d::date AS d,
         COALESCE(SUM(
           CASE WHEN ls.side = 'NO'
                THEN (coalesce_price(d, pp.token_id) * 0 + (pp.avg_entry_price - coalesce_price(d, pp.token_id))) * pp.shares
                ELSE (coalesce_price(d, pp.token_id) - pp.avg_entry_price) * pp.shares
           END
         ),0) AS u
  FROM generate_series((NOW() - ($2::text || ' days')::interval)::date, NOW()::date, '1 day') AS d
  JOIN paper_positions pp ON pp.session_id IN (SELECT id FROM sess) AND pp.shares > 0
  LEFT JOIN LATERAL (
     SELECT FIRST_VALUE(t.side) OVER (PARTITION BY t.session_id, t.token_id ORDER BY t.created_at DESC) AS side
     FROM paper_trades t
     WHERE t.session_id = pp.session_id AND t.token_id = pp.token_id AND t.side IN ('YES','NO')
     LIMIT 1
  ) ls ON true
  GROUP BY 1
)
SELECT
  COALESCE(r.d, u.d) AS date,
  COALESCE(r.r, 0) AS realized,
  COALESCE(u.u, 0) AS unrealized
FROM realized r
FULL OUTER JOIN unreal u USING (d)
ORDER BY date;
`
	// Note: coalesce_price() is not a DB function; we synthesize current price via inline subselects below.
	// Because PostgreSQL can’t call a Go helper inside SQL, we’ll expand the expression using a subquery in code:
	// Replace coalesce_price(d, token) with:
	//   COALESCE(
	//     (SELECT (best_bid+best_ask)/2.0 FROM market_quotes mq WHERE mq.token_id = pp.token_id AND mq.ts::date <= d ORDER BY mq.ts DESC LIMIT 1),
	//     pp.current_price,
	//     pp.avg_entry_price
	//   )
	// We’ll do this by string replacing before query, to keep the CTE structure readable above.
	coalesceExpr := `
COALESCE(
 (SELECT (mq.best_bid+mq.best_ask)/2.0 FROM market_quotes mq WHERE mq.token_id = pp.token_id AND mq.ts::date <= d ORDER BY mq.ts DESC LIMIT 1),
 pp.current_price,
 pp.avg_entry_price
)`
	finalQ := strings.ReplaceAll(q, "coalesce_price(d, pp.token_id)", coalesceExpr)

	rows, err := s.db.QueryContext(ctx, finalQ, strategy, strconv.Itoa(days))
	if err != nil {
		s.log.Error("getStrategyPnL", "err", err)
		writeJSON(w, http.StatusOK, []map[string]interface{}{})
		return
	}
	defer rows.Close()

	type row struct {
		Date       time.Time `json:"date"`
		Realized   float64   `json:"realized"`
		Unrealized float64   `json:"unrealized"`
		Cumulative float64   `json:"cumulative"`
	}
	var out []row
	var cumulative float64
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Date, &r.Realized, &r.Unrealized); err != nil {
			continue
		}
		cumulative += r.Realized + r.Unrealized
		r.Cumulative = cumulative
		out = append(out, r)
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

	// Protected (frontend)
	r.HandleFunc("/api/stats", server.authenticate(server.getStats)).Methods("GET")
	r.HandleFunc("/api/market-events", server.authenticate(server.getMarketEvents)).Methods("GET")
	r.HandleFunc("/api/fills", server.authenticate(server.getFills)).Methods("GET")

	// Strategy lists & details
	r.HandleFunc("/api/strategies", server.authenticate(server.listStrategies)).Methods("GET")
	r.HandleFunc("/api/strategies/{name}/positions", server.authenticate(server.getStrategyPositions)).Methods("GET")
	r.HandleFunc("/api/strategies/{name}/fills", server.authenticate(server.getStrategyFills)).Methods("GET")
	r.HandleFunc("/api/strategies/{name}/pnl", server.authenticate(server.getStrategyPnL)).Methods("GET")

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
