package common

import "strconv"

// ExtractBestBid extracts the best bid price from a Polymarket orderbook
func ExtractBestBid(book map[string]interface{}) float64 {
	if bids, ok := book["bids"].([]interface{}); ok && len(bids) > 0 {
		if bid, ok := bids[0].(map[string]interface{}); ok {
			if priceStr, ok := bid["price"].(string); ok {
				price, _ := strconv.ParseFloat(priceStr, 64)
				return price
			}
		}
	}
	return 0
}

// ExtractBestAsk extracts the best ask price from a Polymarket orderbook
func ExtractBestAsk(book map[string]interface{}) float64 {
	if asks, ok := book["asks"].([]interface{}); ok && len(asks) > 0 {
		if ask, ok := asks[0].(map[string]interface{}); ok {
			if priceStr, ok := ask["price"].(string); ok {
				price, _ := strconv.ParseFloat(priceStr, 64)
				return price
			}
		}
	}
	return 0
}

// CalculateSpread calculates the bid-ask spread as a percentage
func CalculateSpread(bestBid, bestAsk float64) float64 {
	if bestAsk > 0 && bestBid > 0 {
		return (bestAsk - bestBid) / bestAsk
	}
	return 1.0 // Return 100% spread if we can't calculate
}

// TruncateString truncates a string to maxLen characters, adding "..." if truncated
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
