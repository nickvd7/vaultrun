#!/usr/bin/env bash
# Write .env.production for a deployment that is not the local quickstart.
# It does not set VAULTRUN_INSECURE_DEV, so the API refuses to start until
# the production checks pass. Secrets you already set are left as they are.
#
# The stack uses its own Compose project, ports and workspace directory, so
# it can run beside a quickstart that is already on ports 3000 and 8080.
#
#   make prod-env
#   make prod-up
#
# On the host you still run `make gvisor` and, for a hard disk cap,
# `make xfs-workspace`. This file sets DOCKER_RUNTIME=runc when runsc is
# not installed, and MAX_WORKSPACE_MB so a sandbox is paused instead of
# filling the disk. See docs/production-hardening.md.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${1:-$ROOT/.env.production}"
TLS_DIR="$ROOT/deployments/prod"
mkdir -p "$TLS_DIR"

gen() {
  openssl rand -hex 32
}

# set_var KEY VALUE: replace the last assignment, or append one.
set_var() {
  local key="$1" value="$2"
  if grep -Eq "^[[:space:]]*${key}=" "$ENV_FILE"; then
    local tmp
    tmp="$(mktemp)"
    awk -v k="$key" -v v="$value" '
      $0 ~ "^[[:space:]]*" k "=" { line = k "=" v; found = 1 }
      { if (!($0 ~ "^[[:space:]]*" k "=")) print }
      END { if (found) print line }
    ' "$ENV_FILE" > "$tmp"
    mv "$tmp" "$ENV_FILE"
  else
    printf '%s=%s\n' "$key" "$value" >> "$ENV_FILE"
  fi
}

is_placeholder() {
  case "$1" in
    ""|change-me|changeme|change-me-strong-password) return 0 ;;
    *) return 1 ;;
  esac
}

# fill KEY: set a generated value only when the current one is empty or a placeholder.
fill() {
  local key="$1" cur=""
  if [ -f "$ENV_FILE" ]; then
    cur="$(grep -E "^[[:space:]]*${key}=" "$ENV_FILE" | tail -n 1 || true)"
    cur="${cur#*=}"
  fi
  if is_placeholder "$cur"; then
    set_var "$key" "$(gen)"
  fi
}

current() {
  local line=""
  if [ -f "$ENV_FILE" ]; then
    line="$(grep -E "^[[:space:]]*$1=" "$ENV_FILE" | tail -n 1 || true)"
    line="${line#*=}"
  fi
  printf '%s' "$line"
}

# A new file, or a copied .env.example that has not been filled in, gets the
# profile. A later run only fills keys that are still empty, so edits stay.
fresh=0
if [ ! -f "$ENV_FILE" ]; then
  cp "$ROOT/.env.example" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  fresh=1
fi
if [ "$fresh" = 1 ] || { [ "$(current VAULTRUN_INSECURE_DEV)" = "true" ] && [ -z "$(current OPA_POLICY_FILE)" ]; }; then
  mode=set
else
  mode=fill
fi

profile() {
  local key="$1" value="$2"
  if [ "$mode" = "set" ] || [ -z "$(current "$key")" ]; then
    set_var "$key" "$value"
  fi
}

profile VAULTRUN_INSECURE_DEV false
profile OPA_POLICY_FILE /etc/vaultrun/policy.rego
profile OPA_ALLOW_ALL false
profile TLS_DISABLED false
profile TLS_CERT_FILE /etc/vaultrun/tls/api.crt
profile TLS_KEY_FILE /etc/vaultrun/tls/api.key
profile SSO_SESSION_SECURE true
profile SSO_SESSION_MAX_AGE_HOURS 12
profile DOCKER_IMAGE_ALLOWLIST "python:3.12-slim,node:20-slim"
profile MAX_WORKSPACE_MB 2048
profile PORT 18080
profile FRONTEND_PORT 13000
profile POSTGRES_PORT 15432
profile REDIS_PORT 16379
profile PROD_HTTPS_PORT 18443
profile WORKSPACE_HOST_DIR /var/lib/vaultrun-prod/workspaces
profile NETPOLICY_CONTAINER vaultrun-prod-netpolicy
profile COMPOSE_PROJECT_NAME vaultrun-prod
profile VAULTRUN_ENV_FILE ../.env.production
profile CORS_ALLOWED_ORIGINS "http://127.0.0.1:13000,http://localhost:13000,https://127.0.0.1:18443,https://localhost:18443"

if [ "$mode" = "set" ] || [ -z "$(current DOCKER_RUNTIME)" ]; then
  if command -v runsc >/dev/null 2>&1; then
    set_var DOCKER_RUNTIME runsc
  else
    set_var DOCKER_RUNTIME runc
    echo "prod-env: runsc is not installed, so DOCKER_RUNTIME=runc. On the production host run make gvisor and set DOCKER_RUNTIME=runsc." >&2
  fi
fi

fill MASTER_API_KEY
fill METRICS_TOKEN
fill AUDIT_HMAC_KEY
fill REDIS_PASSWORD
fill WEBHOOK_SECRET
fill EVIDENCE_SIGNING_KEY
fill SSO_SESSION_SECRET
fill POSTGRES_PASSWORD

if [ ! -f "$TLS_DIR/api.crt" ] || [ ! -f "$TLS_DIR/api.key" ]; then
  if ! openssl req -x509 -newkey rsa:2048 -sha256 -days 825 -nodes \
    -keyout "$TLS_DIR/api.key" -out "$TLS_DIR/api.crt" \
    -subj "/CN=api" \
    -addext "subjectAltName=DNS:api,DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1; then
    openssl req -x509 -newkey rsa:2048 -sha256 -days 825 -nodes \
      -keyout "$TLS_DIR/api.key" -out "$TLS_DIR/api.crt" \
      -subj "/CN=api" >/dev/null 2>&1
  fi
fi
# 644 so the API user inside the container (uid 65532) can read the key.
# The directory is 700, so other users on the host cannot open it.
chmod 700 "$TLS_DIR"
if [ -f "$TLS_DIR/api.key" ]; then
  chmod 644 "$TLS_DIR/api.key" "$TLS_DIR/api.crt"
fi

chmod 600 "$ENV_FILE"
echo "Wrote $ENV_FILE"
echo "Start it with: make prod-up"
echo "API:        https://127.0.0.1:18080"
echo "Dashboard:  http://127.0.0.1:13000  (API key)"
echo "Dashboard:  https://127.0.0.1:18443 (SSO; the browser warns about Caddy's local certificate)"
echo "Ports 3000, 8080, 5432 and 6379 are not used."
echo "The bundled Postgres stays on the compose network without TLS. For a database on another host, set DB_SSL_MODE=verify-full."
