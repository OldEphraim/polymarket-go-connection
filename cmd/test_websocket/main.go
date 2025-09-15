package main

import (
	"encoding/json"
	"log"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Active NFL token from your search
	tokenID := "22316263718887620010492191615166195580038830908593648773088747272284703322334"

	u := url.URL{Scheme: "wss", Host: "ws-subscriptions-clob.polymarket.com", Path: "/ws/market"}
	log.Printf("Connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	log.Println("✓ Connected successfully")

	done := make(chan struct{})
	messageCount := 0

	// Read messages
	go func() {
		defer close(done)
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				return
			}

			messageCount++

			// Try to parse as JSON to make it readable
			var parsed interface{}
			if err := json.Unmarshal(message, &parsed); err == nil {
				// Only print first few messages in detail
				if messageCount <= 5 {
					pretty, _ := json.MarshalIndent(parsed, "", "  ")
					log.Printf("Message #%d:\n%s", messageCount, pretty)
				} else {
					// Just show a summary
					if m, ok := parsed.(map[string]interface{}); ok {
						if msgType, ok := m["type"].(string); ok {
							log.Printf("Message #%d: type=%s", messageCount, msgType)
						}
					}
				}
			} else {
				log.Printf("Raw message #%d: %s", messageCount, message)
			}
		}
	}()

	// Subscribe to market updates
	sub := map[string]interface{}{
		"type":       "subscribe",
		"channel":    "market",
		"assets_ids": []string{tokenID},
	}

	subJSON, _ := json.Marshal(sub)
	log.Printf("Subscribing to NFL market token: %s...", tokenID[:20])

	err = c.WriteMessage(websocket.TextMessage, subJSON)
	if err != nil {
		log.Println("subscription error:", err)
		return
	}

	// Run for 30 seconds to see if we get updates
	timeout := time.NewTimer(30 * time.Second)

	for {
		select {
		case <-done:
			return
		case <-timeout.C:
			log.Printf("\nTest complete. Received %d messages in 30 seconds", messageCount)
			if messageCount <= 1 {
				log.Println("Only got initial response. Market might not be actively trading.")
				log.Println("Try searching for a more active market (e.g., 'election' or 'bitcoin')")
			}
			return
		case <-interrupt:
			log.Println("Interrupted")
			return
		}
	}
}
