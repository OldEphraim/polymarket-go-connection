package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/OldEphraim/polymarket-go-connection/client"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	// First, let's verify your address matches what you expect
	privateKeyHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		log.Fatal(err)
	}

	address := crypto.PubkeyToAddress(privateKey.PublicKey)
	fmt.Printf("Your address: %s\n", address.Hex())
	fmt.Println("✓ Check this matches your MetaMask address!")
	fmt.Println()

	// Initialize client
	c, err := client.NewPolymarketClient(os.Getenv("PRIVATE_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	// Test 1: Get your allowances (better test than api-keys)
	fmt.Println("Testing API connection with allowances endpoint...")
	allowances, err := c.GetAllowances()
	if err != nil {
		fmt.Printf("❌ Allowances fetch failed: %v\n", err)
	} else {
		fmt.Printf("✓ Allowances: %+v\n", allowances)
	}

	// Test 2: Try a real active market
	// This is "Will Trump's approval rating be above 50% on Jan 31?"
	// You should replace with a current market from Polymarket
	testTokenID := "21742633143463906290569050155826241533067272736897614950488156847949938836455"

	fmt.Printf("\nFetching orderbook for token: %s\n", testTokenID)
	book, err := c.GetOrderBook(testTokenID)
	if err != nil {
		fmt.Printf("❌ Orderbook fetch failed: %v\n", err)
	} else {
		fmt.Printf("✓ Orderbook fetched successfully\n")
		// Pretty print the book
		if bids, ok := book["bids"]; ok {
			fmt.Printf("  Top Bid: %v\n", bids)
		}
		if asks, ok := book["asks"]; ok {
			fmt.Printf("  Top Ask: %v\n", asks)
		}
	}

	// Test 3: Build a VALID order but DON'T submit it
	fmt.Println("\nBuilding test order with valid token ID (NOT SUBMITTING)...")

	// Use the same token ID from above or any valid one
	validTokenID := "21742633143463906290569050155826241533067272736897614950488156847949938836455"

	order := client.Order{
		Salt:          "123456789",
		Maker:         address.Hex(),
		Signer:        address.Hex(),
		Taker:         common.HexToAddress("0x0").Hex(),
		TokenID:       validTokenID, // Use a real token ID
		MakerAmount:   "1000000",    // 1 USDC (6 decimals)
		TakerAmount:   "1010101",    // Roughly 1.01 shares
		Side:          "buy",
		Expiration:    "0",
		Nonce:         "0",
		FeeRateBps:    "0",
		SignatureType: 0,
	}

	err = client.SignOrder(&order, privateKey)
	if err != nil {
		fmt.Printf("❌ Order signing failed: %v\n", err)
		fmt.Printf("   Error details: %+v\n", err)
	} else {
		fmt.Printf("✓ Order signing successful!\n")
		fmt.Printf("  Signature: %s\n", order.Signature[:20]+"...")
	}

	// Test 4: Check if you need to approve USDC first
	fmt.Println("\n=== IMPORTANT NEXT STEPS ===")
	fmt.Println("1. Go to Polymarket.com and connect your wallet")
	fmt.Println("2. Deposit some USDC (even $1 for testing)")
	fmt.Println("3. This will set up the necessary approvals")
	fmt.Println("4. Find a market and get its token ID from the URL or network tab")
	fmt.Println("5. Your address for reference:", address.Hex())

	fmt.Println("\n✅ Connection test complete!")
}
