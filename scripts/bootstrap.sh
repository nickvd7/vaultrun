#!/usr/bin/env bash
# Bootstrap VaultRun: create the first API key using the master key.
set -euo pipefail

API_URL="${VAULTRUN_API_URL:-http://localhost:8080}"
MASTER_KEY="${MASTER_API_KEY:-changeme-master-key}"
KEY_NAME="${1:-default}"

echo "Waiting for API to be ready..."
for i in $(seq 1 30); do
  if curl -sf "$API_URL/health" > /dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "Creating API key: $KEY_NAME"
RESPONSE=$(curl -sf -X POST "$API_URL/api/v1/keys" \
  -H "X-API-Key: $MASTER_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"$KEY_NAME\"}")

echo "$RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$RESPONSE"

KEY=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['key'])" 2>/dev/null || true)
if [ -n "$KEY" ]; then
  echo ""
  echo "✓ API key created. Save this — it won't be shown again:"
  echo ""
  echo "  export VAULTRUN_API_KEY=$KEY"
  echo ""
fi
