#!/usr/bin/env bash
set -euo pipefail

# Make sure the generic is alongside this file
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PURGER="${ROOT_DIR}/scripts/purge_chunks.sh"

# Features: keep ~36h, biggish batch (they’re heavy)
"${PURGER}" market_features "36 hours" 200000

# Quotes: keep ~12h, larger batch (they’re many & small)
"${PURGER}" market_quotes   "12 hours" 500000

# Trades: keep ~24h
"${PURGER}" market_trades   "24 hours" 200000