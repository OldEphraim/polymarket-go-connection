package gatherer

import (
	"context"
)

// These helpers are thin wrappers if you’d like to queue & batch later.
// For now they just call sqlc-backed Store directly.

func persistQuote(ctx context.Context, s Store, q Quote) {
	_ = s.InsertQuote(ctx, q) // TODO(sqlc): handle error/log if desired
}

func persistTrade(ctx context.Context, s Store, t Trade) {
	_ = s.InsertTrade(ctx, t) // TODO(sqlc)
}

func persistFeatures(ctx context.Context, s Store, f FeatureUpdate) {
	_ = s.UpsertFeatures(ctx, f) // TODO(sqlc)
}
