package gatherer

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type cacheEntry struct {
	TokenID      string
	Price24hAgo  sql.NullFloat64
	Volume24hAgo sql.NullFloat64
}

func (g *Gatherer) loadMarketCache() error {
	const pageSize = 1000
	last := ""

	for {
		rows, err := g.store.GetActiveTokenIDsPage(g.ctx, database.GetActiveTokenIDsPageParams{
			TokenID: last,
			Limit:   pageSize,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}

		g.cacheMu.Lock()
		if g.marketCache == nil {
			g.marketCache = make(map[string]*cacheEntry, 4096)
		}
		for _, tok := range rows {
			if _, exists := g.marketCache[tok]; !exists {
				g.marketCache[tok] = &cacheEntry{TokenID: tok}
			}
		}
		g.cacheMu.Unlock()

		last = rows[len(rows)-1]
	}
	g.logger.Info("cache loaded", "count", func() int {
		g.cacheMu.RLock()
		defer g.cacheMu.RUnlock()
		return len(g.marketCache)
	}())
	return nil
}

// Seed assetToToken from DB metadata so WS can subscribe to (almost) everything immediately
func (g *Gatherer) seedAssetsFromDB() error {
	const pageSize = 1000
	last := ""
	totalInserted := 0

	for {
		page, err := g.store.GetAssetMapPage(g.ctx, database.GetAssetMapPageParams{
			TokenID: last,
			Limit:   pageSize,
		})
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}

		g.assetMu.Lock()
		if g.assetToToken == nil {
			g.assetToToken = make(map[string]string, 8192)
		}
		for _, row := range page {
			for _, asset := range toStrings(row.ClobIds) {
				if asset != "" {
					g.assetToToken[asset] = row.TokenID
					totalInserted++
				}
			}
		}
		g.assetMu.Unlock()

		last = page[len(page)-1].TokenID
	}

	g.logger.Info("seeded asset map from DB",
		"assets", func() int {
			g.assetMu.RLock()
			defer g.assetMu.RUnlock()
			return len(g.assetToToken)
		}(),
		"inserted", totalInserted)
	return nil
}

func (g *Gatherer) addClobMap(tokenID string, clobIDs string) {
	if clobIDs == "" {
		return
	}
	ids := decodeAssetIDs(clobIDs)
	if len(ids) == 0 {
		return
	}
	g.assetMu.Lock()
	for _, id := range ids {
		if id != "" {
			g.assetToToken[id] = tokenID
		}
	}
	g.assetMu.Unlock()
}

func decodeAssetIDs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	// JSON array? e.g. ["id1","id2"]
	if strings.HasPrefix(s, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, v := range arr {
				v = strings.TrimSpace(strings.Trim(v, `"'`))
				if v != "" {
					out = append(out, v)
				}
			}
			return out
		}
	}

	// Fallback: CSV (strip stray quotes/brackets)
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, `[]"'`))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (g *Gatherer) wsAssetList() []string {
	g.assetMu.RLock()
	defer g.assetMu.RUnlock()
	out := make([]string, 0, len(g.assetToToken))
	for k := range g.assetToToken {
		out = append(out, k)
	}
	return out
}

func (g *Gatherer) tokenForAsset(assetID string) string {
	g.assetMu.RLock()
	defer g.assetMu.RUnlock()
	return g.assetToToken[assetID]
}

func toStrings(x any) []string {
	switch v := x.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, it := range v {
			if s, ok := it.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []byte:
		// handle jsonb/text[] surfaced as JSON bytes
		var arr []string
		if json.Unmarshal(v, &arr) == nil {
			return arr
		}
	case string:
		// sometimes drivers hand JSON as string
		var arr []string
		if json.Unmarshal([]byte(v), &arr) == nil {
			return arr
		}
	}
	return nil
}
