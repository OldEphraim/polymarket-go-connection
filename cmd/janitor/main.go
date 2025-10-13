package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	var (
		window = flag.String("window", "6 hours", "hot window to keep in Postgres")
		tables = flag.String("tables", "features", "comma list: features,trades,quotes")
	)
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, t := range splitCSV(*tables) {
		switch t {
		case "features":
			call(ctx, db, "SELECT delete_exported_hours_features($1)", *window)
		case "trades":
			// add this once you create the function in a migration
			call(ctx, db, "SELECT delete_exported_hours_trades($1)", *window)
		case "quotes":
			// add this once you create the function in a migration
			call(ctx, db, "SELECT delete_exported_hours_quotes($1)", *window)
		default:
			log.Printf("skip unknown table: %s", t)
		}
	}
}

func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, trim(cur))
				cur = ""
			}
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, trim(cur))
	}
	return out
}
func trim(s string) string {
	i, j := 0, len(s)-1
	for i <= j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j >= i && (s[j] == ' ' || s[j] == '\t') {
		j--
	}
	return s[i : j+1]
}

func call(ctx context.Context, db *sql.DB, sqlstmt, window string) {
	var ok bool
	if err := db.QueryRowContext(ctx, sqlstmt, window).Scan(&ok); err != nil {
		// If function is missing, just log and continue
		log.Printf("janitor call failed (%s): %v", sqlstmt, err)
		return
	}
	log.Printf("janitor %s → %v", sqlstmt, ok)
}
