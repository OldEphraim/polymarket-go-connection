#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./scripts/purge_chunks.sh <TABLE> <INTERVAL> <BATCH_SIZE>
# Example:
#   ./scripts/purge_chunks.sh market_features "36 hours" 200000
#
# Requires: docker compose, psql inside your postgres container service
# Stops when a batch DELETE affects 0 rows.

TABLE="${1:?table required}"
INTERVAL="${2:?interval required (e.g. '36 hours')}"
BATCH="${3:?batch size required (e.g. 200000)}"

echo "[purge] table=${TABLE} interval='${INTERVAL}' batch=${BATCH}"

TOTAL=0
ITER=0

while true; do
  ITER=$((ITER+1))
  # Return one line per deleted row so we can count quickly
  OUT=$(
    docker compose exec -T postgres psql -X -A -t \
      -U a8garber -d polymarket_dev \
      -v "ON_ERROR_STOP=1" \
      -c "WITH doomed AS (
             SELECT ctid
             FROM ${TABLE}
             WHERE ts < now() - interval '${INTERVAL}'
             ORDER BY ts ASC
             LIMIT ${BATCH}
           )
           DELETE FROM ${TABLE} t
           USING doomed d
           WHERE t.ctid = d.ctid
           RETURNING 1;" \
    | wc -l | tr -d '[:space:]'
  )

  # If psql returned nothing, normalize to 0
  DELETED="${OUT:-0}"

  # Sometimes DELETE 0 returns an empty line — guard for that
  if [[ -z "${DELETED}" ]]; then
    DELETED=0
  fi

  TOTAL=$((TOTAL + DELETED))
  echo "[purge] iter=${ITER} deleted_batch=${DELETED} total=${TOTAL}"

  if [[ "${DELETED}" -eq 0 ]]; then
    echo "[purge] done: no more qualifying rows"
    break
  fi
done

echo "[purge] final total deleted: ${TOTAL}"