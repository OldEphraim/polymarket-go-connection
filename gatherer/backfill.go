package gatherer

import "context"

// Optional: a nightly job to backfill trades via REST Data API.
// For now, just a placeholder.
func (g *Gatherer) RunBackfill(ctx context.Context, sinceUTC string) error {
	// TODO(poly): call trades REST to fill gaps; persist with InsertTrade
	return nil
}
