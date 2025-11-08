-- name: MergeMarketFeaturesFrom :exec
SELECT merge_market_features_from(@temp_table::text);