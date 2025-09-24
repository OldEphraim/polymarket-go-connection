package client

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type PolymarketClient struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
	apiURL     string
	client     *http.Client
}

func NewPolymarketClient(privateKeyHex string) (*PolymarketClient, error) {
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0x")
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, err
	}

	return &PolymarketClient{
		privateKey: privateKey,
		address:    crypto.PubkeyToAddress(privateKey.PublicKey),
		apiURL:     "https://clob.polymarket.com",
		client:     &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Get market info without placing orders
func (p *PolymarketClient) GetMarket(conditionID string) (*MarketInfo, error) {
	resp, err := p.client.Get(fmt.Sprintf("%s/markets/%s",
		p.apiURL, conditionID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var market MarketInfo
	json.NewDecoder(resp.Body).Decode(&market)
	return &market, nil
}

// Get order book to verify market data
func (p *PolymarketClient) GetOrderBook(tokenID string) (map[string]interface{}, error) {
	resp, err := p.client.Get(fmt.Sprintf("%s/book?token_id=%s",
		p.apiURL, tokenID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var book map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&book)
	return book, nil
}

func (p *PolymarketClient) GetMarketFromSlug(slug string) (map[string]string, error) {
	// Try different approaches to find the market

	// First, extract potential identifiers from the URL
	urlParts := strings.Split(slug, "/")
	potentialSlug := urlParts[len(urlParts)-1]

	fmt.Printf("Trying slug: %s\n", potentialSlug)

	// Try the direct slug approach
	resp, err := p.client.Get(fmt.Sprintf("%s/markets/%s", p.apiURL, potentialSlug))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("API Response: %s\n", string(body))

	// Try to parse as a market object
	var market struct {
		ConditionID string `json:"condition_id"`
		Tokens      []struct {
			TokenID string  `json:"token_id"`
			Outcome string  `json:"outcome"`
			Price   float64 `json:"price"`
		} `json:"tokens"`
		Question string `json:"question"`
	}

	if err := json.Unmarshal(body, &market); err != nil {
		// Maybe it's in a different format - let's check
		var altFormat map[string]interface{}
		if err2 := json.Unmarshal(body, &altFormat); err2 == nil {
			fmt.Printf("Got response in unexpected format: %+v\n", altFormat)
		}
		return nil, fmt.Errorf("failed to parse: %w", err)
	}

	if len(market.Tokens) == 0 {
		// Try using condition_id if we have it
		if market.ConditionID != "" {
			fmt.Printf("No tokens, but found condition_id: %s\n", market.ConditionID)
			// Try another endpoint
			resp2, err := p.client.Get(fmt.Sprintf("%s/markets?condition_id=%s", p.apiURL, market.ConditionID))
			if err == nil {
				defer resp2.Body.Close()
				body2, _ := io.ReadAll(resp2.Body)
				fmt.Printf("Alternative query result: %s\n", string(body2))
			}
		}
		return nil, fmt.Errorf("no tokens found")
	}

	result := make(map[string]string)
	fmt.Printf("\nMarket: %s\n", market.Question)
	for _, token := range market.Tokens {
		result[token.Outcome] = token.TokenID
		fmt.Printf("%s: %.1f¢ (Token: %s)\n", token.Outcome, token.Price*100, token.TokenID)
	}

	return result, nil
}

func (p *PolymarketClient) DiscoverMarkets() error {
	// Try better queries for current markets
	endpoints := []string{
		"/markets?active=true&closed=false",
		"/markets?active=true&accepting_orders=true&limit=10",
		"/markets?end_date_min=2025-09-06&limit=10", // Markets ending today or later
	}

	for _, endpoint := range endpoints {
		fmt.Printf("\n=== Trying %s ===\n", endpoint)
		resp, err := p.client.Get(p.apiURL + endpoint)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		// Parse the actual structure with "data" wrapper
		var response struct {
			Data []struct {
				Question        string `json:"question"`
				Slug            string `json:"market_slug"`
				ConditionID     string `json:"condition_id"`
				Closed          bool   `json:"closed"`
				Active          bool   `json:"active"`
				AcceptingOrders bool   `json:"accepting_orders"`
				Tokens          []struct {
					TokenID string  `json:"token_id"`
					Outcome string  `json:"outcome"`
					Price   float64 `json:"price"`
				} `json:"tokens"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &response); err != nil {
			fmt.Printf("Parse error: %v\n", err)
			fmt.Printf("Raw response: %.200s...\n", string(body))
			continue
		}

		fmt.Printf("Found %d markets\n", len(response.Data))

		// Show first few markets
		for i, market := range response.Data {
			if i >= 3 {
				break
			}
			fmt.Printf("\nMarket %d:\n", i+1)
			fmt.Printf("  Question: %s\n", market.Question)
			fmt.Printf("  Slug: %s\n", market.Slug)
			fmt.Printf("  Accepting Orders: %v\n", market.AcceptingOrders)
			fmt.Printf("  Closed: %v\n", market.Closed)

			if len(market.Tokens) > 0 {
				fmt.Printf("  Tokens:\n")
				for _, token := range market.Tokens {
					fmt.Printf("    %s: %.1f¢\n", token.Outcome, token.Price*100)
				}
			}
		}
	}

	return nil
}

func (p *PolymarketClient) GetMarketByID(marketID string) (*MarketInfo, error) {
	// Clean up the ID if needed
	if !strings.HasPrefix(marketID, "0x") {
		marketID = "0x" + marketID
	}

	// Try the direct market endpoint
	resp, err := p.client.Get(fmt.Sprintf("%s/markets/%s", p.apiURL, marketID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var market MarketInfo
	if err := json.Unmarshal(body, &market); err != nil {
		// Show what we got for debugging
		fmt.Printf("Raw response: %s\n", string(body))
		return nil, fmt.Errorf("failed to parse: %w", err)
	}

	fmt.Printf("\nMarket: %s\n", market.Question)
	fmt.Printf("Slug: %s\n", market.Slug)
	fmt.Printf("Accepting orders: %v\n", market.AcceptingOrders)

	for _, token := range market.Tokens {
		fmt.Printf("\n%s:\n", token.Outcome)
		fmt.Printf("  Token ID: %s\n", token.TokenID)
		fmt.Printf("  Current price: %.1f¢\n", token.Price*100)
	}

	return &market, nil
}
