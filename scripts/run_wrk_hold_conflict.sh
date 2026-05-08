#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8081}"
THREADS="${THREADS:-4}"
CONNECTIONS="${CONNECTIONS:-20}"
DURATION="${DURATION:-2s}"

if [[ -z "${USER_ID:-}" || -z "${SHOWTIME_ID:-}" || -z "${SEAT_ID:-}" ]]; then
  cat >&2 <<'USAGE'
Missing required env vars.

Required:
  USER_ID       UUID of test user
  SHOWTIME_ID   UUID of showtime
  SEAT_ID       UUID of one AVAILABLE seat

Optional:
  BASE_URL        default http://localhost:8081
  THREADS         default 4
  CONNECTIONS     default 20
  DURATION        default 2s

Example:
  USER_ID=00000000-0000-0000-0000-000000000001 \
  SHOWTIME_ID=<showtime_uuid> \
  SEAT_ID=<available_seat_uuid> \
  ./scripts/run_wrk_hold_conflict.sh
USAGE
  exit 2
fi

if ! command -v wrk >/dev/null 2>&1; then
  echo "wrk is not installed or not in PATH." >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET_URL="${BASE_URL%/}/api/v1/bookings/hold"

echo "Running wrk booking conflict test"
echo "Target: ${TARGET_URL}"
echo "Threads/connections/duration: ${THREADS}/${CONNECTIONS}/${DURATION}"
echo "Expected result: exactly 1 x 201 Created; every other response is 409 Conflict"

export USER_ID SHOWTIME_ID SEAT_ID

wrk \
  -t"${THREADS}" \
  -c"${CONNECTIONS}" \
  -d"${DURATION}" \
  -s"${SCRIPT_DIR}/wrk_hold_same_seat.lua" \
  "${TARGET_URL}"
