#!/usr/bin/env bash
# Wait for the API to answer /health after `make up`. If it does not, print the
# reason from the API log instead of leaving the user with a bare "unhealthy".
# Usage: scripts/wait-healthy.sh [.env] [timeout-seconds] [compose-failed]
# The third argument says `docker compose up` already failed, so this is only a
# short last look and the message must not suggest the API was given that long.
ENV_FILE="${1:-.env}"
TIMEOUT="${2:-90}"
COMPOSE_FAILED="${3:-}"
COMPOSE=(docker compose -f deployments/docker-compose.yml --env-file "$ENV_FILE")
if [ -n "${COMPOSE_EXTRA:-}" ]; then
  COMPOSE+=(-f "$COMPOSE_EXTRA")
fi
PORT="$(sed -n 's/^PORT=//p' "$ENV_FILE" 2>/dev/null | tail -1)"
PORT="${PORT:-8080}"
URL="http://127.0.0.1:${PORT}/health"
TLS_URL="https://127.0.0.1:${PORT}/health"
FE_PORT="$(sed -n 's/^FRONTEND_PORT=//p' "$ENV_FILE" 2>/dev/null | tail -1)"
FE_URL="http://127.0.0.1:${FE_PORT:-3000}/"

# The API answering is not enough: the dashboard process takes a few seconds
# to listen, and `make up` used to return while that port still refused.
for _ in $(seq 1 "$((TIMEOUT / 2))"); do
  api=0
  fe=0
  if curl -fsS "$URL" >/dev/null 2>&1; then
    api=1
  elif curl -fsSk "$TLS_URL" >/dev/null 2>&1; then
    api=1
    URL="$TLS_URL"
  fi
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 "$FE_URL" 2>/dev/null || true)"
  if [ -n "$code" ] && [ "$code" != "000" ]; then
    fe=1
  fi
  if [ "$api" = 1 ] && [ "$fe" = 1 ]; then
    echo "API is healthy at $URL"
    echo "Dashboard is accepting connections at $FE_URL"
    exit 0
  fi
  sleep 2
done

if [ -n "$COMPOSE_FAILED" ]; then
  echo "docker compose reported an error and the stack is not ready (API $URL, dashboard $FE_URL). Reason from the API log:" >&2
else
  echo "The stack was not ready within ${TIMEOUT}s (API $URL, dashboard $FE_URL). Reason from the API log:" >&2
fi
LOG="$("${COMPOSE[@]}" logs --no-color --tail 60 api 2>&1)"
echo "$LOG" | grep -E '"level":"ERROR"' | tail -6 | sed 's/^[^|]*| *//' >&2
if echo "$LOG" | grep -q "insecure configuration"; then
  echo "For local development run: make dev-env && make up" >&2
fi
echo "Last lines from the dashboard log:" >&2
"${COMPOSE[@]}" logs --no-color --tail 20 frontend >&2 || true
echo "Full log: make logs" >&2
exit 1
