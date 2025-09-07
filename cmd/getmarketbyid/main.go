package main

import (
	"fmt"
	"log"
	"os"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/joho/godotenv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/getmarketbyid/main.go [market_id]")
		os.Exit(1)
	}

	godotenv.Load()

	c, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	market, err := c.GetMarketByID(os.Args[1])
	if err != nil {
		log.Fatal("Failed to get market:", err)
	}

	if market != nil && len(market.Tokens) > 0 {
		fmt.Printf("\nReady to bet! Use token IDs above with placebet command.\n")
	}
}
