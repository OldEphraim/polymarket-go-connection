package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Gatherer / Persister metrics
var (
	// Flush operations
	FlushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gatherer_flush_total",
		Help: "Total flush operations by status",
	}, []string{"status"}) // status: success, error

	FlushDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "gatherer_flush_duration_seconds",
		Help:    "Time spent in flush operations",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~16s
	})

	// Records written per flush
	RecordsWritten = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gatherer_records_written_total",
		Help: "Total records written to database",
	}, []string{"table"}) // table: quotes, trades, features

	// Queue depths (gauges)
	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gatherer_queue_depth",
		Help: "Current depth of internal queues",
	}, []string{"queue"}) // queue: quotes, trades, features, events

	// Drops (when queues are full)
	QueueDrops = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gatherer_queue_drops_total",
		Help: "Records dropped due to full queues",
	}, []string{"queue"})

	// Data freshness (set by gatherer or API)
	DataLagSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "data_lag_seconds",
		Help: "Seconds since last record for each data type",
	}, []string{"table"}) // table: quotes, trades, features, events

	// Market counts
	ActiveMarkets = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "active_markets_total",
		Help: "Number of active markets being tracked",
	})

	// Events emitted
	EventsEmitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gatherer_events_emitted_total",
		Help: "Market events emitted by type",
	}, []string{"type"}) // type: price_jump, state_extreme, new_market, etc.
)

// Strategy metrics
var (
	StrategyPositions = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "strategy_positions_open",
		Help: "Number of open positions per strategy",
	}, []string{"strategy"})

	StrategyPnL = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "strategy_pnl_dollars",
		Help: "Current P&L in dollars (realized + unrealized)",
	}, []string{"strategy", "type"}) // type: realized, unrealized

	StrategyTrades = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "strategy_trades_total",
		Help: "Total trades executed per strategy",
	}, []string{"strategy", "side"}) // side: buy, sell
)

// System metrics
var (
	DiskUsagePercent = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "disk_usage_percent",
		Help: "Disk usage percentage",
	})

	DiskFreeMB = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "disk_free_mb",
		Help: "Free disk space in MB",
	})

	DatabaseSizeMB = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "database_size_mb",
		Help: "PostgreSQL database size in MB",
	})

	PartitionCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "partition_count",
		Help: "Number of partitions per table",
	}, []string{"table"})

	// Service health (1 = healthy, 0 = unhealthy)
	ServiceHealth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "service_health",
		Help: "Service health status (1=healthy, 0=unhealthy)",
	}, []string{"service"}) // service: gatherer, api, janitor, archiver, healthmonitor
)

// Archive metrics
var (
	ArchiveBacklogHours = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "archive_backlog_hours",
		Help: "Hours of data waiting to be archived",
	}, []string{"table"})

	ArchiveJobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "archive_jobs_total",
		Help: "Archive jobs by status",
	}, []string{"table", "status"}) // status: success, error
)

// Janitor metrics
var (
	PartitionsDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "janitor_partitions_dropped_total",
		Help: "Partitions dropped by janitor",
	}, []string{"table"})

	RowsDeleted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "janitor_rows_deleted_total",
		Help: "Rows deleted by janitor",
	}, []string{"table"})
)
