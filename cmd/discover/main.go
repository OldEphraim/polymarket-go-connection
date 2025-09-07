package main

import (
	"log"
	"os"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	c, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	if err := c.DiscoverMarkets(); err != nil {
		log.Fatal(err)
	}
}
