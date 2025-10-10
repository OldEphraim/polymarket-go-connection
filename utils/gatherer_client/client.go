package gatherer_client

import (
	"context"
	"encoding/json"
	"time"

	"github.com/OldEphraim/polymarket-go-connection/db"
	"github.com/OldEphraim/polymarket-go-connection/gatherer"
	"github.com/OldEphraim/polymarket-go-connection/internal/database"
)

type Client struct {
	store        *db.Store
	lastEventID  int32
	pollInterval time.Duration
}

func New(store *db.Store) *Client {
	return &Client{
		store:        store,
		pollInterval: 5 * time.Second,
		lastEventID:  0,
	}
}

func (c *Client) StreamEvents(ctx context.Context) <-chan gatherer.MarketEvent {
	eventChan := make(chan gatherer.MarketEvent, 100)

	go func() {
		ticker := time.NewTicker(c.pollInterval)
		defer ticker.Stop()
		defer close(eventChan)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				events, err := c.store.GetMarketEventsSince(ctx, c.lastEventID)
				if err != nil {
					// you can log here if you want
					continue
				}
				for _, e := range events {
					eventChan <- convertDBEventToMarketEvent(e)
					if e.ID > c.lastEventID {
						c.lastEventID = e.ID
					}
				}
			}
		}
	}()

	return eventChan
}

func convertDBEventToMarketEvent(dbEvent database.MarketEvent) gatherer.MarketEvent {
	// metadata
	var metadata map[string]interface{}
	if dbEvent.Metadata.Valid {
		_ = json.Unmarshal(dbEvent.Metadata.RawMessage, &metadata)
	} else {
		metadata = map[string]interface{}{}
	}

	// numeric values are NullFloat64 after migration 003
	oldValue := 0.0
	newValue := 0.0
	if dbEvent.OldValue.Valid {
		oldValue = dbEvent.OldValue.Float64
	}
	if dbEvent.NewValue.Valid {
		newValue = dbEvent.NewValue.Float64
	}

	// event type
	evType := ""
	if dbEvent.EventType.Valid {
		evType = dbEvent.EventType.String
	}

	// timestamp (likely sql.NullTime)
	ts := time.Time{}
	if dbEvent.DetectedAt.Valid {
		ts = dbEvent.DetectedAt.Time
	}

	return gatherer.MarketEvent{
		Type:      gatherer.MarketEventType(evType),
		TokenID:   dbEvent.TokenID,
		EventID:   "", // not stored in market_events
		Timestamp: ts,
		OldValue:  oldValue,
		NewValue:  newValue,
		Metadata:  metadata,
	}
}
