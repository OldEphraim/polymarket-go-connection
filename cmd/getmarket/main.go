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
		fmt.Println("Usage: go run cmd/getmarket/main.go [URL or slug]")
		os.Exit(1)
	}

	godotenv.Load()

	c, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	tokens, err := c.GetMarketFromSlug(os.Args[1])
	if err != nil {
		log.Fatal("Failed to get market:", err)
	}

	fmt.Println("\nToken IDs found:")
	for outcome, tokenID := range tokens {
		fmt.Printf("%s: %s\n", outcome, tokenID)
	}
}
