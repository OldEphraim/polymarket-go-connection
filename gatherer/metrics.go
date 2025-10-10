package gatherer

import (
	"time"
)

func (g *Gatherer) metricsLoop() {
	defer g.wg.Done()
	t := time.NewTicker(1 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
			g.cacheMu.RLock()
			cacheSize := len(g.marketCache)
			g.cacheMu.RUnlock()
			g.logger.Info("metrics",
				"scans", g.scansPerformed,
				"markets_found", g.marketsFound,
				"events_emitted", g.eventsEmitted,
				"cache_size", cacheSize)
		}
	}
}
