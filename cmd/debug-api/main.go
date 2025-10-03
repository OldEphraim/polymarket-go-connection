package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	// Fetch one event to see the structure
	url := "https://gamma-api.polymarket.com/events?closed=false&limit=1"

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error fetching: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading body: %v\n", err)
		return
	}

	// Save raw response to file for inspection
	os.WriteFile("api_response.json", body, 0644)
	fmt.Println("Raw response saved to api_response.json")

	// Try to parse as raw JSON to see structure
	var raw []interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		fmt.Printf("Error parsing JSON: %v\n", err)
		return
	}

	// Pretty print first event
	if len(raw) > 0 {
		pretty, _ := json.MarshalIndent(raw[0], "", "  ")
		fmt.Println("\nFirst event structure:")
		fmt.Println(string(pretty))
	}
}
