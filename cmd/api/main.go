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
            (SELECT COUNT(DISTINCT token_id) FROM paper_positions) as open_positions,
            (SELECT COALESCE(SUM(unrealized_pnl), 0) FROM paper_positions) as total_pnl,
            (SELECT COUNT(DISTINCT s.name) FROM trading_sessions ts JOIN strategies s ON ts.strategy_id = s.id) as strategies_count
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
	limit := getIntParam(r, "limit", 50)

	query := `
        SELECT 
            po.id,
            po.token_id,
            COALESCE(s.name, 'unknown') as strategy,
            po.side,
            po.price as entry_price,
            po.size,
            po.status,
            po.created_at as entry_time,
            po.filled_at as exit_time,
            COALESCE(ms.question, po.token_id) as market_id,
            COALESCE(pp.unrealized_pnl, 0) as pnl
        FROM paper_orders po
        LEFT JOIN trading_sessions ts ON po.session_id = ts.id
        LEFT JOIN strategies s ON ts.strategy_id = s.id
        LEFT JOIN market_scans ms ON po.token_id = ms.token_id
        LEFT JOIN paper_positions pp ON pp.session_id = po.session_id 
            AND pp.token_id = po.token_id
        ORDER BY po.created_at DESC
        LIMIT $1
    `

	rows, err := s.db.Query(query, limit)
	if err != nil {
		log.Printf("Error querying trades: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{}) // Return empty array on error
		return
	}
	defer rows.Close()

	trades := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var tokenID, strategy, side, status string
		var entryPrice, size, pnl float64
		var marketID sql.NullString
		var entryTime time.Time
		var exitTime sql.NullTime

		err := rows.Scan(
			&id,
			&tokenID,
			&strategy,
			&side,
			&entryPrice,
			&size,
			&status,
			&entryTime,
			&exitTime,
			&marketID,
			&pnl,
		)
		if err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		trade := map[string]interface{}{
			"id":          id,
			"token_id":    tokenID,
			"strategy":    strategy,
			"market_id":   marketID.String,
			"side":        side,
			"entry_price": entryPrice,
			"size":        size,
			"status":      status,
			"entry_time":  entryTime,
			"pnl":         pnl,
		}

		if exitTime.Valid {
			trade["exit_time"] = exitTime.Time
			trade["exit_price"] = entryPrice + (pnl / size) // Calculate exit price from PnL
		}

		trades = append(trades, trade)
	}

	// Return array directly, not wrapped in object
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trades)
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
	// Can't do real performance without proper position tracking
	// Let's at least show what strategies are running
	query := `
        SELECT 
            s.name as strategy,
            COUNT(DISTINCT p.token_id) as positions,
            COALESCE(SUM(p.unrealized_pnl), 0) as unrealized_pnl
        FROM strategies s
        LEFT JOIN trading_sessions ts ON s.id = ts.strategy_id
        LEFT JOIN paper_positions p ON ts.id = p.session_id
        GROUP BY s.name
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
		var positions int64
		var unrealizedPnL float64

		err := rows.Scan(&strategy, &positions, &unrealizedPnL)
		if err != nil {
			continue
		}

		performances = append(performances, map[string]interface{}{
			"strategy":       strategy,
			"positions":      positions,
			"unrealized_pnl": unrealizedPnL,
		})
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
