#!/usr/bin/env bash
# Wait for the API to answer /health after `make up`. If it does not, print the
# reason from the API log instead of leaving the user with a bare "unhealthy".
# Usage: scripts/wait-healthy.sh [.env] [timeout-seconds] [compose-failed]
# The third argument says `docker compose up` already failed, so this is only a
# short last look and the message must not suggest the API was given that long.
ENV_FILE="${1:-.env}"
TIMEOUT="${2:-90}"
COMPOSE_FAILED="${3:-}"
PORT="$(sed -n 's/^PORT=//p' "$ENV_FILE" 2>/dev/null | tail -1)"
URL="http://localhost:${PORT:-8080}/health"

for _ in $(seq 1 "$((TIMEOUT / 2))"); do
  if curl -fsS "$URL" >/dev/null 2>&1; then
    echo "API is healthy at $URL"
    exit 0
  fi
  sleep 2
done

if [ -n "$COMPOSE_FAILED" ]; then
  echo "docker compose reported an error and the API does not answer at $URL. Reason from the API log:" >&2
else
  echo "The API did not answer at $URL within ${TIMEOUT}s. Reason from the API log:" >&2
fi
LOG="$(docker compose -f deployments/docker-compose.yml --env-file "$ENV_FILE" logs --no-color --tail 60 api 2>&1)"
echo "$LOG" | grep -E '"level":"ERROR"' | tail -6 | sed 's/^[^|]*| *//' >&2
if echo "$LOG" | grep -q "insecure configuration"; then
  echo "For local development run: make dev-env && make up" >&2
fi
echo "Full log: make logs" >&2
exit 1
