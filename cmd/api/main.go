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
	"strings"
	"sync"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type StatsResponse struct {
	ActiveMarkets       int64     `json:"active_markets"`
	Events24h           int64     `json:"events_24h"`
	OpenPositions       int64     `json:"open_positions"`
	TotalPnL            float64   `json:"total_pnl"`
	StrategiesCount     int64     `json:"strategies_count"`
	QuotesLagSec        float64   `json:"quotes_lag_sec"`
	TradesLagSec        float64   `json:"trades_lag_sec"`
	FeaturesLagSec      float64   `json:"features_lag_sec"`
	DBSize              string    `json:"db_size"`
	FeaturesPerMinute   int64     `json:"features_per_minute"`
	IngestQuotesPerMin  float64   `json:"ingest_quotes_per_min"`
	IngestTradesPerMin  float64   `json:"ingest_trades_per_min"`
	ApproxQuotesTotal   int64     `json:"approx_quotes_total"`
	ApproxTradesTotal   int64     `json:"approx_trades_total"`
	ApproxFeaturesTotal int64     `json:"approx_features_total"`
	GeneratedAt         time.Time `json:"generated_at"`
}

type APIServer struct {
	store  *db.Store
	db     *sql.DB
	apiKey string
	log    *slog.Logger

	statsMu     sync.RWMutex
	latestStats *StatsResponse
}

type StreamLagSnapshot struct {
	QuotesLagSec   float64   `json:"quotes_lag_sec"`
	TradesLagSec   float64   `json:"trades_lag_sec"`
	FeaturesLagSec float64   `json:"features_lag_sec"`
	GeneratedAt    time.Time `json:"generated_at"`
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

// parseFloatDefault returns the parsed float64 value if possible,
// otherwise it returns def.
func parseFloatDefault(s string, def float64) float64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

// computeStats runs one big SQL query and caches the result in memory.
// Even if this takes a few seconds, it's fine: call it periodically from
// a goroutine and serve the cached value under /api/stats.
func (s *APIServer) computeStats(ctx context.Context) (*StatsResponse, error) {
	const q = `
WITH
  -- Basic, now index-accelerated stats
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
  db_size AS (
    SELECT pg_size_pretty(pg_database_size(current_database())) AS size
  ),

  -- Approximate totals via pg_class.reltuples (catalog only; very fast)
  quotes_est AS (
    SELECT COALESCE(SUM(c.reltuples),0)::bigint AS n
    FROM pg_class c
    JOIN pg_namespace nsp ON nsp.oid = c.relnamespace
    WHERE nsp.nspname = 'public'
      AND c.relname LIKE 'market_quotes_p%'
  ),
  trades_est AS (
    SELECT COALESCE(SUM(c.reltuples),0)::bigint AS n
    FROM pg_class c
    JOIN pg_namespace nsp ON nsp.oid = c.relnamespace
    WHERE nsp.nspname = 'public'
      AND c.relname LIKE 'market_trades_p%'
  ),
  features_est AS (
    SELECT COALESCE(SUM(c.reltuples),0)::bigint AS n
    FROM pg_class c
    JOIN pg_namespace nsp ON nsp.oid = c.relnamespace
    WHERE nsp.nspname = 'public'
      AND c.relname LIKE 'market_features_p%'
  ),

  -- Partition span metadata from your helper
  quotes_span AS (
    SELECT * FROM poly_partition_span('market_quotes_p%')
  ),
  trades_span AS (
    SELECT * FROM poly_partition_span('market_trades_p%')
  ),
  features_span AS (
    SELECT * FROM poly_partition_span('market_features_p%')
  ),

  -- Approximate per-minute ingest from partition spans.
  quotes_rates AS (
    SELECT
      q.n AS total,
      CASE
        WHEN s.oldest_partition IS NULL OR s.newest_partition IS NULL
             OR s.newest_partition <= s.oldest_partition
        THEN 0::float8
        ELSE q.n / GREATEST(
          LEAST(
            EXTRACT(EPOCH FROM (s.newest_partition - s.oldest_partition)) / 60.0,
            60.0
          ),
          1.0
        )
      END AS per_min
    FROM quotes_est q
    CROSS JOIN quotes_span s
  ),
  trades_rates AS (
    SELECT
      q.n AS total,
      CASE
        WHEN s.oldest_partition IS NULL OR s.newest_partition IS NULL
             OR s.newest_partition <= s.oldest_partition
        THEN 0::float8
        ELSE q.n / GREATEST(
          LEAST(
            EXTRACT(EPOCH FROM (s.newest_partition - s.oldest_partition)) / 60.0,
            60.0
          ),
          1.0
        )
      END AS per_min
    FROM trades_est q
    CROSS JOIN trades_span s
  ),
  features_rates AS (
    SELECT
      q.n AS total,
      CASE
        WHEN s.oldest_partition IS NULL OR s.newest_partition IS NULL
             OR s.newest_partition <= s.oldest_partition
        THEN 0::float8
        ELSE q.n / GREATEST(
          LEAST(
            EXTRACT(EPOCH FROM (s.newest_partition - s.oldest_partition)) / 60.0,
            60.0
          ),
          1.0
        )
      END AS per_min
    FROM features_est q
    CROSS JOIN features_span s
  )

SELECT
  (SELECT n FROM active_markets)                                 AS active_markets,
  (SELECT n FROM events_24h)                                     AS events_24h,
  (SELECT n FROM open_positions)                                 AS open_positions,
  (SELECT r FROM realized) + (SELECT unreal FROM open_positions) AS total_pnl,
  (SELECT n FROM strategies_active)                              AS strategies_count,

  (SELECT size FROM db_size)                                     AS db_size,

  COALESCE((SELECT per_min FROM features_rates), 0)::bigint      AS features_per_minute,
  COALESCE((SELECT per_min FROM quotes_rates),   0)              AS ingest_quotes_per_min,
  COALESCE((SELECT per_min FROM trades_rates),   0)              AS ingest_trades_per_min,

  COALESCE((SELECT total FROM quotes_rates),   0)::bigint        AS approx_quotes_total,
  COALESCE((SELECT total FROM trades_rates),   0)::bigint        AS approx_trades_total,
  COALESCE((SELECT total FROM features_rates), 0)::bigint        AS approx_features_total,

  now()                                                          AS generated_at
;
`

	row := s.db.QueryRowContext(ctx, q)

	var res StatsResponse
	if err := row.Scan(
		&res.ActiveMarkets,
		&res.Events24h,
		&res.OpenPositions,
		&res.TotalPnL,
		&res.StrategiesCount,
		&res.DBSize,
		&res.FeaturesPerMinute,
		&res.IngestQuotesPerMin,
		&res.IngestTradesPerMin,
		&res.ApproxQuotesTotal,
		&res.ApproxTradesTotal,
		&res.ApproxFeaturesTotal,
		&res.GeneratedAt,
	); err != nil {
		return nil, err
	}

	return &res, nil
}

func (s *APIServer) refreshStatsOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	snap, err := s.computeStats(ctx)
	if err != nil {
		s.log.Error("stats refresh failed", "err", err)
		return
	}

	s.statsMu.Lock()
	s.latestStats = snap
	s.statsMu.Unlock()

	s.log.Info("stats refreshed",
		"generated_at", snap.GeneratedAt,
		"active_markets", snap.ActiveMarkets,
		"events_24h", snap.Events24h,
	)
}

func (s *APIServer) startStatsRefresher(interval time.Duration) {
	// fire in the background; process exit will stop it
	go func() {
		// initial fetch (will take ~30s the first time)
		s.refreshStatsOnce()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.refreshStatsOnce()
		}
	}()
}

// ---------- endpoints ----------

// /api/stats — high-level system & trading stats (from cached snapshot)
func (s *APIServer) getStats(w http.ResponseWriter, r *http.Request) {
	s.statsMu.RLock()
	snap := s.latestStats
	s.statsMu.RUnlock()

	if snap == nil {
		// Still warming up; you can return 503 or a small JSON
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "stats snapshot not ready yet",
		})
		return
	}

	writeJSON(w, http.StatusOK, snap)
}

// /api/market-events — live event feed
func (s *APIServer) getMarketEvents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	limit, offset := parseLimitOffset(r, 100)

	q := r.URL.Query()
	hours := atoiDefault(q.Get("hours"), 0) // 0 = no time window
	eventType := q.Get("type")
	minRet := parseFloatDefault(q.Get("min_ret"), 0) // 0 = no min filter

	base := `
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
`

	where := []string{}
	args := []any{limit, offset}
	argIdx := 3

	// Optional time window: ONLY applied if hours > 0
	if hours > 0 {
		where = append(where,
			fmt.Sprintf("me.detected_at > now() - ($%d::text || ' hours')::interval", argIdx),
		)
		args = append(args, strconv.Itoa(hours))
		argIdx++
	}

	// Optional event_type filter
	if eventType != "" {
		where = append(where,
			fmt.Sprintf("me.event_type = $%d", argIdx),
		)
		args = append(args, eventType)
		argIdx++
	}

	// Optional "big move" filter for state_extreme (our price-jump proxy)
	if eventType == "state_extreme" && minRet > 0 {
		where = append(where, fmt.Sprintf(`
(
  (me.metadata->>'ret_1m' IS NOT NULL
   AND abs((me.metadata->>'ret_1m')::double precision) >= $%d)
  OR
  (me.old_value IS NOT NULL
   AND me.new_value IS NOT NULL
   AND me.old_value > 0
   AND abs((me.new_value - me.old_value) / me.old_value) >= $%d)
)
`, argIdx, argIdx))
		args = append(args, minRet)
		argIdx++
	}

	if len(where) > 0 {
		base += "WHERE " + strings.Join(where, " AND ") + "\n"
	}

	base += "ORDER BY me.detected_at DESC\nLIMIT $1 OFFSET $2;"

	rows, err := s.db.QueryContext(ctx, base, args...)
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

		if err := rows.Scan(
			&e.TokenID,
			&evType,
			&oldV,
			&newV,
			&e.DetectedAt,
			&qn,
			&raw,
		); err != nil {
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

// /api/stream-lag — measures how much the system is lagging
func (s *APIServer) getStreamLag(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()

	queryLag := func(table string) (float64, error) {
		q := fmt.Sprintf(`
			SELECT GREATEST(EXTRACT(EPOCH FROM (now() - max(ts))), 0) AS lag_sec
			FROM %s
			WHERE ts > now() - interval '1 day';
		`, table)

		var lagSec float64
		row := s.db.QueryRowContext(ctx, q)
		if err := row.Scan(&lagSec); err != nil {
			return 0, err
		}
		return lagSec, nil
	}

	quotesLag, err := queryLag("market_quotes")
	if err != nil {
		s.log.Error("streamLag quotes", "err", err)
		writeErr(w, http.StatusInternalServerError, "lag query failed")
		return
	}

	tradesLag, err := queryLag("market_trades")
	if err != nil {
		s.log.Error("streamLag trades", "err", err)
		writeErr(w, http.StatusInternalServerError, "lag query failed")
		return
	}

	featuresLag, err := queryLag("market_features")
	if err != nil {
		s.log.Error("streamLag features", "err", err)
		writeErr(w, http.StatusInternalServerError, "lag query failed")
		return
	}

	snap := StreamLagSnapshot{
		QuotesLagSec:   quotesLag,
		TradesLagSec:   tradesLag,
		FeaturesLagSec: featuresLag,
		GeneratedAt:    time.Now().UTC(),
	}

	writeJSON(w, http.StatusOK, snap)
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

	server.startStatsRefresher(10 * time.Minute)

	r := mux.NewRouter()

	// Public
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}).Methods("GET")

	// Protected (frontend)
	r.HandleFunc("/api/stats", server.authenticate(server.getStats)).Methods("GET")
	r.HandleFunc("/api/market-events", server.authenticate(server.getMarketEvents)).Methods("GET")
	r.HandleFunc("/api/stream-lag", server.authenticate(server.getStreamLag)).Methods("GET")
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
