# CLAUDE.md - Polymarket Go Connection

## Project Overview

A real-time Polymarket trading infrastructure system that collects market data, detects anomalies, executes paper trading strategies, and provides monitoring/archival capabilities.

## Quick Reference

```bash
# SSH to EC2
ssh -i ~/.ssh/polymarket-key.pem ec2-user@3.145.119.158

# Project path on EC2
cd /home/ec2-user/polymarket-go-connection

# Docker commands (use dash on EC2)
docker-compose up -d
docker-compose logs -f gatherer
docker-compose restart gatherer

# Local development
docker compose up -d postgres
go run ./cmd/gatherer/main.go
go run ./cmd/api/main.go
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Polymarket API                               │
│            (gamma-api.polymarket.com + WebSocket)                   │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                           GATHERER                                   │
│  • REST polling (30s) + WebSocket streams                           │
│  • Normalizes quotes, trades, features                              │
│  • Detects events (price_jump, volume_spike, etc.)                  │
│  • COPY-based batch persistence                                      │
│  • Exposes /metrics on port 9090                                    │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         POSTGRESQL                                   │
│  Tables: market_quotes, market_trades, market_features, market_events│
│  All time-series tables use hourly partitioning (_pYYYYMMDDHH)      │
└─────────────────────────────────────────────────────────────────────┘
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐   ┌──────────────┐   ┌──────────────┐
│   STRATEGIES │   │     API      │   │   ARCHIVER   │
│ (Paper Trade)│   │  (port 8080) │   │   (to S3)    │
└──────────────┘   └──────────────┘   └──────────────┘
        │
        ▼
┌──────────────────────────────────────────────────────────────────────┐
│                      MONITORING STACK                                 │
│  Prometheus (9091) → Grafana (3000) → AlertManager (9093)           │
└──────────────────────────────────────────────────────────────────────┘
```

## Directory Structure

```
polymarket-go-connection/
├── cmd/                    # Service entry points
│   ├── api/               # HTTP API server (:8080)
│   ├── gatherer/          # Data collection (:9090 metrics, :6060 pprof)
│   ├── archiver/          # S3 archival (:7070 pprof)
│   ├── janitor/           # Data retention cleanup
│   └── healthmonitor/     # System health monitoring
├── gatherer/               # Core gatherer library
│   ├── gatherer.go        # Main orchestration
│   ├── persist.go         # COPY batch writer
│   ├── feature_engine.go  # Derived metrics calculation
│   ├── detectors.go       # Event detection logic
│   └── ws/                # WebSocket handlers
├── strategies/             # Trading strategy implementations
│   ├── momentum/          # Price momentum following
│   ├── mean_reversion/    # Z-score based reversion
│   ├── copycat/           # Follow other traders
│   └── whale_follow/      # Track large volume
├── internal/
│   ├── database/          # SQLC-generated queries
│   └── metrics/           # Prometheus metric definitions
├── utils/
│   ├── paper_trading/     # Paper trading engine
│   ├── strategy_persistence/ # Session management
│   └── ...                # Various utilities
├── sql/
│   ├── migrations/        # Goose migrations (001-025+)
│   └── queries/           # SQLC query definitions
├── archiver/              # S3 archival logic
├── monitoring/            # Prometheus/Grafana/AlertManager configs
├── configs/               # Strategy JSON configs (*-prod.json)
├── main.go                # Orchestrator (launches strategies)
├── docker-compose.yml     # Full stack deployment
└── Dockerfile             # Multi-stage build
```

## Key Services

### Gatherer (cmd/gatherer)
- **Purpose**: Collects real-time market data from Polymarket
- **Ports**: 9090 (metrics), 6060 (pprof)
- **Data Flow**: REST poll every 30s → normalize → batch COPY to Postgres
- **Key Files**:
  - `gatherer/persist.go` - Batch writer with queue management
  - `gatherer/feature_engine.go` - Calculates returns, volatility, z-scores
  - `gatherer/detectors.go` - Emits events on anomalies

### API (cmd/api)
- **Purpose**: HTTP API for frontend and monitoring
- **Port**: 8080
- **Key Endpoints**:
  - `GET /health` - Health check
  - `GET /metrics` - Prometheus metrics
  - `GET /api/stats` - Cached system statistics
  - `GET /api/stream-lag` - Data freshness (quotes/trades/features lag)
  - `GET /api/market-events` - Live event feed
  - `GET /api/strategies` - Strategy leaderboard
  - `GET /api/strategies/{name}/pnl` - P&L breakdown

### Strategies (strategies/*)
- **Purpose**: Paper trading algorithms
- **Config**: JSON files in `configs/` (e.g., `momentum-prod.json`)
- **Framework**:
  - Uses `utils/paper_trading` for order execution
  - Uses `utils/strategy_persistence` for session state
  - Reads market data from gatherer API (:6060)

### Janitor (cmd/janitor)
- **Purpose**: Manages data retention and disk space
- **Behavior**:
  - Drops old partitions based on retention windows
  - Emergency mode when disk < 1.5GB free
  - Pauses gatherer at 90% disk, resumes at 85%

### Archiver (cmd/archiver)
- **Purpose**: Archives data to S3 before janitor deletes
- **Format**: Gzip JSON with S3 path `{prefix}/dt={date}/hour={HH}/part-00000.json.gz`

## Database Schema

### Core Tables (all hourly partitioned)

```sql
-- Top-of-book snapshots
market_quotes (id, token_id, ts, best_bid, best_ask, bid_size1, ask_size1, spread_bps, mid)

-- Trade prints
market_trades (id, token_id, ts, price, size, aggressor, trade_id)

-- Derived features (PK: token_id, ts)
market_features (token_id, ts, ret_1m, ret_5m, vol_1m, sigma_5m, zscore_5m, imbalance_top, ...)

-- Detected events
market_events (token_id, event_type, old_value, new_value, metadata, detected_at)
```

### Paper Trading Tables

```sql
strategies (id, name, config, initial_balance, active)
trading_sessions (id, strategy_id, start_balance, current_balance, started_at, ended_at)
paper_positions (id, session_id, token_id, shares, avg_entry_price, unrealized_pnl)
paper_trades (id, session_id, token_id, side, price, shares, realized_pnl)
```

### Partitioning

All time-series tables use hourly partitions named `{table}_p{YYYYMMDDHH}`:
- Enables efficient retention (DROP PARTITION vs DELETE)
- Managed by janitor (drop old) and SQL trigger (create new)

## Environment Variables

```bash
# Required
DATABASE_URL=postgresql://user:pass@localhost:5432/polymarket?sslmode=disable
API_KEY=<32-char hex for X-API-Key header>

# Gatherer
GATHERER_METRICS_PORT=9090
GATHERER_SCAN_INTERVAL=30s
GATHERER_USE_WS=true
GOMEMLIMIT=700MiB

# Archiver
ARCHIVE_S3_BUCKET=polymarket-archive-prod-bucket
AWS_REGION=us-east-2

# Runtime
RUN_TYPE=prod  # affects config file glob: *-{RUN_TYPE}.json
```

## Monitoring

### Access Points
- **Grafana**: http://EC2_IP:3000 (admin/admin)
- **Prometheus**: http://EC2_IP:9091
- **AlertManager**: http://EC2_IP:9093

### Key Metrics
```promql
# Data freshness (critical for detecting stuck gatherer)
data_lag_seconds{table="quotes"}
data_lag_seconds{table="trades"}
data_lag_seconds{table="features"}

# Write throughput
rate(gatherer_records_written_total[1m]) * 60

# Flush health
gatherer_flush_total{status="error"}
gatherer_flush_duration_seconds

# Queue drops (indicates backpressure)
gatherer_queue_drops_total
```

### Alerts Configured
- **QuotesDataStale**: lag > 5min (warning), > 15min (critical)
- **GathererFlushErrors**: any flush errors
- **QueueDropsDetected**: records being dropped
- **DiskSpaceCritical**: > 90% disk usage
- **NoRecordsWritten**: 0 writes for 5+ minutes

## Common Operations

### Deploy Changes
```bash
# Local: commit and push
git add -A && git commit -m "message"

# EC2: pull and rebuild
ssh -i ~/.ssh/polymarket-key.pem ec2-user@3.145.119.158
cd /home/ec2-user/polymarket-go-connection
git pull
docker-compose build --no-cache
docker-compose up -d
```

### Database Migrations
```bash
# Apply all pending
goose -dir sql/migrations postgres "$DATABASE_URL" up

# Rollback one
goose -dir sql/migrations postgres "$DATABASE_URL" down

# Check status
goose -dir sql/migrations postgres "$DATABASE_URL" status
```

### Regenerate SQLC
```bash
# After editing sql/queries/*.sql
sqlc generate
```

### Regenerate Schema Dump
```bash
pg_dump -h localhost -U postgres -d polymarket_dev \
  --schema-only --no-owner --no-privileges \
  --exclude-table='market_quotes_p*' \
  --exclude-table='market_trades_p*' \
  --exclude-table='market_features_p*' \
  --exclude-table='market_events_p*' \
  > sql/schema.sql
```

### Debug Stuck Gatherer
```bash
# Check current lag
curl -s http://localhost:8080/api/stream-lag | jq

# Check recent writes
psql -c "SELECT table_name, count(*) FROM (
  SELECT 'quotes' as table_name FROM market_quotes WHERE ts > NOW() - INTERVAL '10 min'
  UNION ALL
  SELECT 'trades' FROM market_trades WHERE ts > NOW() - INTERVAL '10 min'
  UNION ALL
  SELECT 'features' FROM market_features WHERE ts > NOW() - INTERVAL '10 min'
) x GROUP BY table_name"

# Restart gatherer
docker-compose restart gatherer
```

### Check Partition Health
```bash
psql -c "SELECT relname, pg_size_pretty(pg_relation_size(oid))
FROM pg_class WHERE relname LIKE 'market_quotes_p%'
ORDER BY relname DESC LIMIT 5"
```

## Strategy Configuration

Strategy configs live in `configs/{name}-{RUN_TYPE}.json`:

```json
{
  "name": "momentum-prod",
  "duration": "infinite",
  "momentum_threshold": 0.02,
  "hold_period": "10m",
  "max_spread_bps": 200,
  "min_vol_surge": 1.3,
  "min_abs_ret_1m": 0.003
}
```

The orchestrator (`main.go`) launches strategies matching `configs/*-${RUN_TYPE}.json`.

## Known Issues & Gotchas

1. **Gatherer can get stuck**: If you see high lag for quotes/trades but features is fine, the COPY batcher may be hung. Restart gatherer.

2. **Disk fills fast**: market_events and market_quotes are high-volume. Janitor needs to run continuously. Monitor `disk_usage_percent` metric.

3. **Partition explosion**: Each hour creates 4 new partitions. Janitor drops old ones, but verify with `SELECT count(*) FROM pg_tables WHERE tablename LIKE 'market_%_p%'`.

4. **Memory pressure**: Gatherer and archiver have GOMEMLIMIT set. If OOM-killed, check `docker stats` and adjust limits.

5. **EC2 uses `docker-compose`** (with dash), local may use `docker compose` (space).

## Useful Queries

```sql
-- Check data recency
SELECT
  (SELECT max(ts) FROM market_quotes) as latest_quote,
  (SELECT max(ts) FROM market_trades) as latest_trade,
  (SELECT max(ts) FROM market_features) as latest_feature;

-- Partition sizes
SELECT relname, pg_size_pretty(pg_relation_size(oid))
FROM pg_class
WHERE relname LIKE 'market_%_p%'
ORDER BY pg_relation_size(oid) DESC
LIMIT 10;

-- Strategy performance
SELECT s.name, ts.current_balance - ts.start_balance as pnl
FROM strategies s
JOIN trading_sessions ts ON ts.strategy_id = s.id
WHERE ts.ended_at IS NULL
ORDER BY pnl DESC;

-- Recent events
SELECT event_type, count(*), max(detected_at)
FROM market_events
WHERE detected_at > NOW() - INTERVAL '1 hour'
GROUP BY event_type
ORDER BY count(*) DESC;
```

## File Locations Summary

| What | Where |
|------|-------|
| Service entry points | `cmd/{service}/main.go` |
| Gatherer core logic | `gatherer/*.go` |
| Database queries | `sql/queries/*.sql` |
| Generated DB code | `internal/database/*.go` |
| Migrations | `sql/migrations/*.sql` |
| Strategy implementations | `strategies/{name}/main.go` |
| Strategy configs | `configs/*-prod.json` |
| Prometheus metrics | `internal/metrics/metrics.go` |
| Monitoring configs | `monitoring/*.yml` |
| Grafana dashboard | `monitoring/grafana/provisioning/dashboards/polymarket.json` |
| Docker setup | `docker-compose.yml`, `Dockerfile` |
