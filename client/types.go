package client

type Order struct {
	Salt          string `json:"salt"`
	Maker         string `json:"maker"`
	Signer        string `json:"signer"`
	Taker         string `json:"taker"`
	TokenID       string `json:"tokenId"`
	MakerAmount   string `json:"makerAmount"`
	TakerAmount   string `json:"takerAmount"`
	Side          string `json:"side"`
	Expiration    string `json:"expiration"`
	Nonce         string `json:"nonce"`
	FeeRateBps    string `json:"feeRateBps"`
	SignatureType int    `json:"signatureType"`
	Signature     string `json:"signature"`
}

type MarketInfo struct {
	ConditionID     string `json:"condition_id"`
	QuestionID      string `json:"question_id"`
	Question        string `json:"question"`
	Slug            string `json:"market_slug"`
	AcceptingOrders bool   `json:"accepting_orders"`
	Active          bool   `json:"active"`
	Closed          bool   `json:"closed"`
	Tokens          []struct {
		TokenID string  `json:"token_id"`
		Outcome string  `json:"outcome"`
		Price   float64 `json:"price"`
	} `json:"tokens"`
}
