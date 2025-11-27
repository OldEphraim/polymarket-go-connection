package gatherer

import "time"

type StatsWindows struct {
	Ret1m       time.Duration `json:"ret_1m"`
	Ret5m       time.Duration `json:"ret_5m"`
	Vol1m       time.Duration `json:"vol_1m"`
	Vol5m       time.Duration `json:"vol_5m"`
	Sigma5m     time.Duration `json:"sigma_5m"`
	FeatCadence time.Duration `json:"feat_cadence"` // how often to emit features (e.g., 5s)
}

type Thresholds struct {
	// Existing
	MaxSpreadBps int     `json:"max_spread_bps"`
	ZMin         float64 `json:"z_min"`
	VolSurgeMin  float64 `json:"vol_surge_min"` // vol_1m / avg_vol_5m
	ImbMin       float64 `json:"imb_min"`       // |imbalance| gate

	// New (used by detectors & features)
	MaxAbsSpread   float64       `json:"max_abs_spread"`  // e.g., 0.02 = 2¢
	MinLiquidity   float64       `json:"min_liquidity"`   // arbitrary units from API/liquidity model
	DebounceWindow time.Duration `json:"debounce_window"` // e.g., 30s
	SigmaFloor     float64       `json:"sigma_floor"`     // e.g., 0.01 = 1¢

	// Legacy-jump knobs
	PriceJumpMinPct   float64       `json:"price_jump_min_pct"`  // e.g. 0.05 => 5%
	PriceJumpMinAbs   float64       `json:"price_jump_min_abs"`  // e.g. 0.01 => 1¢
	PriceJumpDebounce time.Duration `json:"price_jump_debounce"` // per-token cool-down for price jumps
}

type Config struct {
	BaseURL      string        `json:"base_url"`
	WebsocketURL string        `json:"websocket_url"`
	ScanInterval time.Duration `json:"scan_interval"`
	UseWebsocket bool          `json:"use_websocket"`

	// TODO(book-levels): when multi-level book is supported, re-enable:
	// BookLevels int `json:"book_levels"`

	Stats      StatsWindows `json:"stats_windows"`
	Thresholds Thresholds   `json:"thresholds"`

	// Optional: tune the size of the in-process publish queue
	EventQueueSize int `json:"event_queue_size"`

	EmitNewMarkets   bool `json:"emit_new_markets"`
	EmitPriceJumps   bool `json:"emit_price_jumps"`
	EmitVolumeSpikes bool `json:"emit_volume_spikes"`
}

func DefaultConfig() *Config {
	return &Config{
		BaseURL:      "https://gamma-api.polymarket.com",
		WebsocketURL: "wss://ws-subscriptions-clob.polymarket.com/ws/market",
		ScanInterval: 30 * time.Second,
		UseWebsocket: true,
		// BookLevels: 1, // see TODO above

		Stats: StatsWindows{
			Ret1m:       time.Minute,
			Ret5m:       5 * time.Minute,
			Vol1m:       time.Minute,
			Vol5m:       5 * time.Minute,
			Sigma5m:     5 * time.Minute,
			FeatCadence: 5 * time.Second,
		},
		Thresholds: Thresholds{
			MaxSpreadBps:      150, // a bit looser but still sane
			ZMin:              2.0, // only used by StateExtreme, makes MR a bit chattier
			VolSurgeMin:       1.5, // easier to cross than 2.0
			ImbMin:            0.2, // still fine, currently inert
			MaxAbsSpread:      0.02,
			MinLiquidity:      50,
			DebounceWindow:    30 * time.Second,
			SigmaFloor:        0.01,
			PriceJumpMinPct:   0.05,
			PriceJumpMinAbs:   0.01,
			PriceJumpDebounce: 5 * time.Minute,
		},

		EventQueueSize:   100000,
		EmitNewMarkets:   true,
		EmitPriceJumps:   true,
		EmitVolumeSpikes: true,
	}
}
