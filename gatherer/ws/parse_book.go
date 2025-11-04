package ws

import (
	"encoding/json"
)

type bookMsg struct {
	TokenID string               `json:"token_id"`
	AssetID string               `json:"asset_id"`
	Bids    [][2]json.RawMessage `json:"bids"` // [["0.52","100"], ...]
	Asks    [][2]json.RawMessage `json:"asks"`
}

func topOfBook(levels [][2]json.RawMessage) float64 {
	if len(levels) == 0 || len(levels[0]) < 1 {
		return 0
	}
	// Often strings; try string → float
	var s string
	if err := json.Unmarshal(levels[0][0], &s); err == nil {
		if f, err := strconvParseFloat(s, 64); err == nil {
			return f
		}
	}
	// Fallback numeric
	var f float64
	_ = json.Unmarshal(levels[0][0], &f)
	return f
}
