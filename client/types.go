package client

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
