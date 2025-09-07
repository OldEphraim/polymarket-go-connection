package main

import (
	"fmt"
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

	// Example: Place a $1 bet at 99¢ on a market
	// You need to get the tokenID from Polymarket's website
	tokenID := "YOUR_TOKEN_ID_HERE" // Get this from the market

	order, err := c.CreateAndSubmitOrder(
		tokenID,
		"buy", // buying "Yes" shares
		0.99,  // at 99 cents
		1.0,   // for $1 USDC
	)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Order placed! ID: %s\n", order.Salt)
}
