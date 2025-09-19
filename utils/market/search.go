package market

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// SearchResult represents a market search result from Polymarket
type SearchResult struct {
	Question      string
	Active        bool
	Volume24hr    float64
	TokenIDs      []string
	Outcomes      []string
	OutcomePrices []float64
	Tokens        []TokenResult
}

// TokenResult represents a token within a market
type TokenResult struct {
	TokenID string
	Outcome string
	Price   float64
}

// Searcher provides methods for searching Polymarket markets
type Searcher struct {
	scriptPath string
	maxResults int
}

// NewSearcher creates a new market searcher
func NewSearcher() *Searcher {
	return &Searcher{
		scriptPath: "./run_search.sh",
		maxResults: 10,
	}
}

// NewSearcherWithPath creates a searcher with a custom script path
func NewSearcherWithPath(scriptPath string) *Searcher {
	return &Searcher{
		scriptPath: scriptPath,
		maxResults: 10,
	}
}

// Search performs a market search with the given query
func (s *Searcher) Search(query string) ([]SearchResult, error) {
	return s.SearchWithLimit(query, s.maxResults)
}

// SearchWithLimit performs a market search with a custom result limit
func (s *Searcher) SearchWithLimit(query string, limit int) ([]SearchResult, error) {
	cmd := exec.Command(s.scriptPath, query, "--json", "--limit", strconv.Itoa(limit))

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("search script failed: %w", err)
	}

	return s.parseResults(output)
}

// SearchMultiple performs searches for multiple queries
func (s *Searcher) SearchMultiple(queries []string) ([]SearchResult, error) {
	var allResults []SearchResult

	for _, query := range queries {
		results, err := s.Search(query)
		if err != nil {
			// Log error but continue with other queries
			continue
		}
		allResults = append(allResults, results...)
	}

	return allResults, nil
}

// SearchWithFilters performs a search and applies filters
func (s *Searcher) SearchWithFilters(query string, filters SearchFilters) ([]SearchResult, error) {
	results, err := s.Search(query)
	if err != nil {
		return nil, err
	}

	return s.applyFilters(results, filters), nil
}

// SearchFilters contains filtering criteria for search results
type SearchFilters struct {
	MinVolume24hr float64
	MaxVolume24hr float64
	ActiveOnly    bool
	MinPrice      float64
	MaxPrice      float64
	MaxResults    int
}

// DefaultFilters returns sensible default filters
func DefaultFilters() SearchFilters {
	return SearchFilters{
		MinVolume24hr: 10000, // $10k minimum volume
		ActiveOnly:    true,
		MinPrice:      0.05, // 5% minimum
		MaxPrice:      0.95, // 95% maximum
		MaxResults:    20,
	}
}

// FilterResult checks if a single result passes the filters
func (f *SearchFilters) FilterResult(result SearchResult) bool {
	if f.ActiveOnly && !result.Active {
		return false
	}

	if f.MinVolume24hr > 0 && result.Volume24hr < f.MinVolume24hr {
		return false
	}

	if f.MaxVolume24hr > 0 && result.Volume24hr > f.MaxVolume24hr {
		return false
	}

	return true
}

// FilterToken checks if a token passes the price filters
func (f *SearchFilters) FilterToken(token TokenResult) bool {
	if f.MinPrice > 0 && token.Price < f.MinPrice {
		return false
	}

	if f.MaxPrice > 0 && token.Price > f.MaxPrice {
		return false
	}

	return true
}

// Private methods

func (s *Searcher) parseResults(data []byte) ([]SearchResult, error) {
	var raw struct {
		Markets []struct {
			Question      string   `json:"question"`
			Active        bool     `json:"active"`
			Volume24hr    float64  `json:"volume24hr"`
			TokenIDs      []string `json:"token_ids"`
			Outcomes      []string `json:"outcomes"`
			OutcomePrices []string `json:"outcome_prices"`
		} `json:"markets"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w", err)
	}

	var results []SearchResult
	for _, m := range raw.Markets {
		result := SearchResult{
			Question:   m.Question,
			Active:     m.Active,
			Volume24hr: m.Volume24hr,
			TokenIDs:   m.TokenIDs,
			Outcomes:   m.Outcomes,
			Tokens:     make([]TokenResult, 0),
		}

		// Parse prices and create token results
		for i := 0; i < len(m.TokenIDs) && i < len(m.Outcomes) && i < len(m.OutcomePrices); i++ {
			price, _ := strconv.ParseFloat(m.OutcomePrices[i], 64)
			result.OutcomePrices = append(result.OutcomePrices, price)

			result.Tokens = append(result.Tokens, TokenResult{
				TokenID: m.TokenIDs[i],
				Outcome: m.Outcomes[i],
				Price:   price,
			})
		}

		results = append(results, result)
	}

	return results, nil
}

func (s *Searcher) applyFilters(results []SearchResult, filters SearchFilters) []SearchResult {
	var filtered []SearchResult

	for _, result := range results {
		if !filters.FilterResult(result) {
			continue
		}

		// Filter tokens within the market
		var filteredTokens []TokenResult
		for _, token := range result.Tokens {
			if filters.FilterToken(token) {
				filteredTokens = append(filteredTokens, token)
			}
		}

		// Only include market if it has valid tokens
		if len(filteredTokens) > 0 {
			result.Tokens = filteredTokens
			filtered = append(filtered, result)

			if filters.MaxResults > 0 && len(filtered) >= filters.MaxResults {
				break
			}
		}
	}

	return filtered
}

// SearchService provides a higher-level interface for market searching
type SearchService struct {
	searcher *Searcher
	cache    map[string][]SearchResult
	filters  SearchFilters
}

// NewSearchService creates a new search service with caching
func NewSearchService() *SearchService {
	return &SearchService{
		searcher: NewSearcher(),
		cache:    make(map[string][]SearchResult),
		filters:  DefaultFilters(),
	}
}

// SetFilters updates the default filters
func (ss *SearchService) SetFilters(filters SearchFilters) {
	ss.filters = filters
}

// FindMarkets searches for markets using the default queries
func (ss *SearchService) FindMarkets(queries []string) ([]SearchResult, error) {
	var allResults []SearchResult

	for _, query := range queries {
		// Check cache first
		if cached, exists := ss.cache[query]; exists {
			allResults = append(allResults, cached...)
			continue
		}

		// Search and apply filters
		results, err := ss.searcher.SearchWithFilters(query, ss.filters)
		if err != nil {
			continue
		}

		// Cache results
		ss.cache[query] = results
		allResults = append(allResults, results...)
	}

	return allResults, nil
}

// ClearCache clears the search cache
func (ss *SearchService) ClearCache() {
	ss.cache = make(map[string][]SearchResult)
}
