package gatherer

import (
	"encoding/json"
	"strings"
)

func (g *Gatherer) loadMarketCache() error {
	rows, err := g.store.GetActiveMarketScans(g.ctx, 10000)
	if err != nil {
		return err
	}
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	for i := range rows {
		g.marketCache[rows[i].TokenID] = &rows[i]
	}
	g.logger.Info("cache loaded", "count", len(g.marketCache))
	return nil
}

// Seed assetToToken from DB metadata so WS can subscribe to (almost) everything immediately
func (g *Gatherer) seedAssetsFromDB() error {
	rows, err := g.store.GetActiveMarketScans(g.ctx, 1_000_000) // ok to overshoot; sqlc impl should page/limit internally if needed
	if err != nil {
		return err
	}

	count := 0
	g.assetMu.Lock()
	defer g.assetMu.Unlock()

	for i := range rows {
		r := rows[i]
		if !r.Metadata.Valid || r.TokenID == "" {
			continue
		}

		var meta map[string]any
		if err := json.Unmarshal(r.Metadata.RawMessage, &meta); err != nil {
			continue
		}

		raw, ok := meta["clob_token_ids"]
		if !ok || raw == nil {
			continue
		}

		switch v := raw.(type) {
		case []any: // properly stored array
			for _, elt := range v {
				if s, ok := elt.(string); ok && s != "" {
					g.assetToToken[s] = r.TokenID
					count++
				}
			}
		case string: // JSON-stringified array or CSV
			for _, s := range decodeAssetIDs(v) {
				if s != "" {
					g.assetToToken[s] = r.TokenID
					count++
				}
			}
		default:
			// ignore scalars/unknown
		}
	}
	g.logger.Info("seeded asset map from DB", "assets", len(g.assetToToken), "inserted", count)
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
