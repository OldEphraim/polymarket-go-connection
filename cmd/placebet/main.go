package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/joho/godotenv"
)

func main() {
	// Define command line flags
	tokenID := flag.String("token", "", "Token ID of the market (required)")
	side := flag.String("side", "buy", "Side of the order: buy or sell (default: buy)")
	price := flag.Float64("price", 0, "Price per share in USDC (0.01 to 0.99, required)")
	size := flag.Float64("size", 0, "Size of order in USDC (required)")
	dryRun := flag.Bool("dry", false, "Dry run - don't actually submit the order")

	flag.Parse()

	// Validate required flags
	if *tokenID == "" {
		fmt.Println("Error: -token flag is required")
		fmt.Println("\nUsage:")
		flag.PrintDefaults()
		fmt.Println("\nExample:")
		fmt.Println("  go run cmd/placebet/main.go -token=12345... -price=0.99 -size=1.0")
		fmt.Println("  go run cmd/placebet/main.go -token=12345... -side=sell -price=0.50 -size=5.0 -dry")
		os.Exit(1)
	}

	if *price <= 0 || *price >= 1 {
		log.Fatal("Error: price must be between 0.01 and 0.99")
	}

	if *size <= 0 {
		log.Fatal("Error: size must be greater than 0")
	}

	// Load environment
	godotenv.Load()

	// Initialize client
	c, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		log.Fatal("Failed to initialize client:", err)
	}

	// Display order details
	fmt.Println("=== Order Details ===")
	fmt.Printf("Token ID: %s\n", *tokenID)
	fmt.Printf("Side: %s\n", *side)
	fmt.Printf("Price: $%.2f per share\n", *price)
	fmt.Printf("Size: $%.2f USDC\n", *size)

	if *side == "buy" {
		shares := *size / *price
		fmt.Printf("Expected shares: %.2f\n", shares)
	} else {
		proceeds := *size * *price
		fmt.Printf("Expected proceeds: $%.2f USDC\n", proceeds)
	}

	if *dryRun {
		fmt.Println("\n🔍 DRY RUN MODE - Not submitting order")

		// Could still build and sign the order to test
		fmt.Println("✓ Order validation passed")
		fmt.Println("✓ Would submit order to Polymarket")
		return
	}

	// Confirmation prompt for real orders
	fmt.Print("\nConfirm order submission? (y/n): ")
	var confirm string
	fmt.Scanln(&confirm)
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Order cancelled")
		return
	}

	// Place the order
	fmt.Println("\n📡 Submitting order...")
	order, err := c.CreateAndSubmitOrder(
		*tokenID,
		*side,
		*price,
		*size,
	)

	if err != nil {
		log.Fatal("Failed to place order:", err)
	}

	fmt.Printf("\n🎉 Order placed successfully!\n")
	fmt.Printf("Order ID: %s\n", order.Salt)
	fmt.Printf("Signature: %s...\n", order.Signature[:20])

	// TODO: Could add order tracking here
	fmt.Println("\nView your order at: https://polymarket.com/profile")
}
