package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
	db     *sql.DB // Direct database connection for custom queries
	apiKey string
}

// Middleware to check API key
func (s *APIServer) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key != s.apiKey {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Helper to get query param as int with default
func getIntParam(r *http.Request, name string, defaultValue int) int {
	param := r.URL.Query().Get(name)
	if param == "" {
		return defaultValue
	}
	val, err := strconv.Atoi(param)
	if err != nil {
		return defaultValue
	}
	return val
}

// GET /api/stats
func (s *APIServer) getStats(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT 
            (SELECT COUNT(*) FROM market_scans WHERE is_active = true) as active_markets,
            (SELECT COUNT(*) FROM market_events WHERE detected_at > NOW() - INTERVAL '24 hours') as events_24h,
            (SELECT COUNT(DISTINCT token_id) FROM paper_positions WHERE status = 'open') as open_positions,
            (SELECT COALESCE(SUM(pnl), 0) FROM paper_positions WHERE status = 'closed') as total_pnl,
            (SELECT COUNT(DISTINCT strategy) FROM paper_positions) as strategies_count
    `

	var stats struct {
		ActiveMarkets   int64   `json:"active_markets"`
		Events24h       int64   `json:"events_24h"`
		OpenPositions   int64   `json:"open_positions"`
		TotalPnL        float64 `json:"total_pnl"`
		StrategiesCount int64   `json:"strategies_count"`
	}

	row := s.db.QueryRow(query)
	err := row.Scan(&stats.ActiveMarkets, &stats.Events24h, &stats.OpenPositions, &stats.TotalPnL, &stats.StrategiesCount)
	if err != nil {
		log.Printf("Error in getStats: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GET /api/trades
func (s *APIServer) getTrades(w http.ResponseWriter, r *http.Request) {
	// Query parameters
	limit := getIntParam(r, "limit", 50)
	offset := getIntParam(r, "offset", 0)
	strategy := r.URL.Query().Get("strategy")
	status := r.URL.Query().Get("status")

	// Build query
	query := `
        SELECT 
            COALESCE(p.token_id, '') as token_id,
            p.strategy,
            p.side,
            p.entry_price,
            p.exit_price,
            p.pnl,
            p.status,
            p.created_at,
            m.question
        FROM paper_positions p
        LEFT JOIN market_scans m ON p.token_id = m.token_id
        WHERE 1=1
    `

	args := []interface{}{}
	argCount := 0

	if strategy != "" {
		argCount++
		query += fmt.Sprintf(" AND p.strategy = $%d", argCount)
		args = append(args, strategy)
	}

	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND p.status = $%d", argCount)
		args = append(args, status)
	}

	query += " ORDER BY p.created_at DESC"

	// Add pagination
	argCount++
	query += fmt.Sprintf(" LIMIT $%d", argCount)
	args = append(args, limit)

	argCount++
	query += fmt.Sprintf(" OFFSET $%d", argCount)
	args = append(args, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	trades := []map[string]interface{}{}
	for rows.Next() {
		var tokenID string
		var strategy, side, status, question sql.NullString
		var entryPrice, exitPrice, pnl sql.NullFloat64
		var createdAt time.Time

		err := rows.Scan(
			&tokenID,
			&strategy,
			&side,
			&entryPrice,
			&exitPrice,
			&pnl,
			&status,
			&createdAt,
			&question,
		)
		if err != nil {
			log.Printf("Scan error: %v", err)
			continue
		}

		trade := map[string]interface{}{
			"token_id":    tokenID,
			"strategy":    strategy.String,
			"side":        side.String,
			"entry_price": entryPrice.Float64,
			"exit_price":  exitPrice.Float64,
			"pnl":         pnl.Float64,
			"status":      status.String,
			"created_at":  createdAt,
			"question":    question.String,
		}

		trades = append(trades, trade)
	}

	response := map[string]interface{}{
		"trades": trades,
		"pagination": map[string]interface{}{
			"limit":  limit,
			"offset": offset,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /api/market-events
func (s *APIServer) getMarketEvents(w http.ResponseWriter, r *http.Request) {
	limit := getIntParam(r, "limit", 100)
	offset := getIntParam(r, "offset", 0)
	hours := getIntParam(r, "hours", 1)

	query := fmt.Sprintf(`
        SELECT 
            me.token_id,
            me.event_type,
            me.old_value,
            me.new_value,
            me.detected_at,
            ms.question
        FROM market_events me
        LEFT JOIN market_scans ms ON me.token_id = ms.token_id
        WHERE me.detected_at > NOW() - INTERVAL '%d hours'
        ORDER BY me.detected_at DESC
        LIMIT $1 OFFSET $2
    `, hours)

	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	events := []map[string]interface{}{}
	for rows.Next() {
		var tokenID string
		var eventType, oldValue, newValue, question sql.NullString
		var detectedAt time.Time

		err := rows.Scan(
			&tokenID,
			&eventType,
			&oldValue,
			&newValue,
			&detectedAt,
			&question,
		)
		if err != nil {
			continue
		}

		// Parse numeric values
		oldVal, _ := strconv.ParseFloat(oldValue.String, 64)
		newVal, _ := strconv.ParseFloat(newValue.String, 64)

		event := map[string]interface{}{
			"token_id":    tokenID,
			"event_type":  eventType.String,
			"old_value":   oldVal,
			"new_value":   newVal,
			"detected_at": detectedAt,
			"question":    question.String,
		}

		events = append(events, event)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// GET /api/strategy-performance
func (s *APIServer) getStrategyPerformance(w http.ResponseWriter, r *http.Request) {
	query := `
        SELECT 
            COALESCE(strategy, 'unknown') as strategy,
            COUNT(*) as total_trades,
            SUM(CASE WHEN pnl > 0 THEN 1 ELSE 0 END) as winning_trades,
            COALESCE(SUM(pnl), 0) as total_pnl,
            COALESCE(AVG(pnl), 0) as avg_pnl
        FROM paper_positions
        WHERE status = 'closed'
        GROUP BY strategy
    `

	rows, err := s.db.Query(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	performances := []map[string]interface{}{}
	for rows.Next() {
		var strategy string
		var totalTrades, winningTrades int64
		var totalPnL, avgPnL float64

		err := rows.Scan(
			&strategy,
			&totalTrades,
			&winningTrades,
			&totalPnL,
			&avgPnL,
		)
		if err != nil {
			continue
		}

		winRate := float64(0)
		if totalTrades > 0 {
			winRate = float64(winningTrades) / float64(totalTrades) * 100
		}

		perf := map[string]interface{}{
			"strategy":       strategy,
			"total_trades":   totalTrades,
			"winning_trades": winningTrades,
			"win_rate":       winRate,
			"total_pnl":      totalPnL,
			"avg_pnl":        avgPnL,
		}

		performances = append(performances, perf)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(performances)
}

func main() {
	godotenv.Load()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		log.Fatal("API_KEY environment variable must be set")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}

	// Get both SQLC store and raw DB connection
	store, err := db.NewStore(dbURL)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Also open a direct database connection for custom queries
	rawDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer rawDB.Close()

	server := &APIServer{
		store:  store,
		db:     rawDB,
		apiKey: apiKey,
	}

	r := mux.NewRouter()

	// Public
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Protected endpoints
	r.HandleFunc("/api/stats", server.authenticate(server.getStats)).Methods("GET")
	r.HandleFunc("/api/trades", server.authenticate(server.getTrades)).Methods("GET")
	r.HandleFunc("/api/market-events", server.authenticate(server.getMarketEvents)).Methods("GET")
	r.HandleFunc("/api/strategy-performance", server.authenticate(server.getStrategyPerformance)).Methods("GET")

	corsHandler := handlers.CORS(
		handlers.AllowedOrigins([]string{"*"}), // Update for production!
		handlers.AllowedMethods([]string{"GET", "OPTIONS"}),
		handlers.AllowedHeaders([]string{"Content-Type", "X-API-Key"}),
	)

	handler := corsHandler(r)

	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("API server starting on port %s", port)
	log.Printf("Test with: curl -H 'X-API-Key: %s' http://localhost:%s/api/stats", apiKey, port)

	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}
