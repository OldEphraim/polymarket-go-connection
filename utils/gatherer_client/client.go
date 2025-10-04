package gatherer_client

import (
	"context"
	"encoding/json"
	"fmt"
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
				// Get new events since last check
				events, err := c.store.GetMarketEventsSince(ctx, c.lastEventID)
				if err != nil {
					// Log error but continue polling
					continue
				}

				for _, e := range events {
					eventChan <- convertDBEventToMarketEvent(e)
					// Update last event ID to track progress
					if e.ID > c.lastEventID {
						c.lastEventID = e.ID
					}
				}
			}
		}
	}()

	return eventChan
}

// convertDBEventToMarketEvent converts database event to gatherer event
func convertDBEventToMarketEvent(dbEvent database.MarketEvent) gatherer.MarketEvent {
	// Parse metadata from JSON
	var metadata map[string]interface{}
	if dbEvent.Metadata.Valid {
		json.Unmarshal(dbEvent.Metadata.RawMessage, &metadata)
	} else {
		metadata = make(map[string]interface{})
	}

	// Parse numeric values from SQL NullString
	oldValue := 0.0
	newValue := 0.0

	if dbEvent.OldValue.Valid {
		fmt.Sscanf(dbEvent.OldValue.String, "%f", &oldValue)
	}
	if dbEvent.NewValue.Valid {
		fmt.Sscanf(dbEvent.NewValue.String, "%f", &newValue)
	}

	// Convert event type (handle NULL case)
	eventType := ""
	if dbEvent.EventType.Valid {
		eventType = dbEvent.EventType.String
	}

	// Handle nullable timestamp
	ts := time.Time{}
	if dbEvent.DetectedAt.Valid {
		ts = dbEvent.DetectedAt.Time
	}

	return gatherer.MarketEvent{
		Type:      gatherer.EventType(eventType),
		TokenID:   dbEvent.TokenID,
		EventID:   "", // Not stored in market_events table
		Timestamp: ts,
		OldValue:  oldValue,
		NewValue:  newValue,
		Metadata:  metadata,
	}
}

// SetPollInterval allows adjusting the polling frequency
func (c *Client) SetPollInterval(interval time.Duration) {
	c.pollInterval = interval
}

// GetLastEventID returns the last processed event ID (useful for debugging)
func (c *Client) GetLastEventID() int32 {
	return c.lastEventID
}
