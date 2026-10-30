#!/usr/bin/env bash
# Tell users why the API may refuse to start (secure by default since v0.4.0).
# Mirrors Config.ProductionProblems in internal/config/config.go.
# Usage: scripts/check-env.sh [.env]
ENV_FILE="${1:-.env}"
[ -f "$ENV_FILE" ] || exit 0

# Last assignment of KEY in the file, without quotes, a trailing comment or CR.
env_value() {
  local line
  line=$(grep -E "^[[:space:]]*$1=" "$ENV_FILE" | tail -n 1) || true
  line="${line#*=}"
  line="${line%%[[:space:]]#*}"
  line="${line%$'\r'}"
  line="${line#\"}"; line="${line%\"}"
  line="${line#\'}"; line="${line%\'}"
  printf '%s' "$line"
}

is_true() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    1|t|true) return 0 ;;
    *) return 1 ;;
  esac
}

# WORKSPACE_QUOTA=xfs: the API refuses to start, in any mode, when the
# workspace directory is not on XFS with project quotas.
if [ "$(env_value WORKSPACE_QUOTA)" = "xfs" ] && command -v findmnt >/dev/null 2>&1; then
  ws="$(env_value WORKSPACE_HOST_DIR)"; ws="${ws:-/var/lib/vaultrun/workspaces}"
  if [ "$(findmnt -n -o FSTYPE --target "$ws" 2>/dev/null)" != "xfs" ] ||
     ! findmnt -n -o OPTIONS --target "$ws" | tr ',' '\n' | grep -Eq '^(prjquota|pquota)'; then
    echo "Note: WORKSPACE_QUOTA=xfs, but $ws is not on XFS mounted with prjquota, so the API will" >&2
    echo "      refuse to start. Run make xfs-workspace (docs/production-hardening.md)." >&2
  fi
fi

if ! grep -Eq '^[[:space:]]*VAULTRUN_INSECURE_DEV=' "$ENV_FILE"; then
  cat >&2 <<MSG
Note: $ENV_FILE has no VAULTRUN_INSECURE_DEV. Since v0.4.0 the API refuses to start unless an OPA
policy, TLS and METRICS_TOKEN are configured (docs/production-hardening.md).
  Local use:   make dev-env   (adds VAULTRUN_INSECURE_DEV=true to $ENV_FILE)
  Deployment:  configure OPA_POLICY_FILE, TLS_CERT_FILE/TLS_KEY_FILE (or ACME_DOMAIN), METRICS_TOKEN
MSG
  exit 0
fi

# Local development opt-out: the API starts with a warning.
is_true "$(env_value VAULTRUN_INSECURE_DEV)" && exit 0

# Production mode (VAULTRUN_INSECURE_DEV is set but not true): the API exits at
# startup for each problem below, so say so before `docker compose up`.
problems=()
if [ -z "$(env_value OPA_POLICY_FILE)" ] && ! is_true "$(env_value OPA_ALLOW_ALL)"; then
  problems+=("no OPA policy: set OPA_POLICY_FILE, or OPA_ALLOW_ALL=true to accept AllowAll explicitly")
fi
if { [ -z "$(env_value TLS_CERT_FILE)" ] || [ -z "$(env_value TLS_KEY_FILE)" ]; } &&
   [ -z "$(env_value ACME_DOMAIN)" ] && ! is_true "$(env_value TLS_DISABLED)"; then
  problems+=("TLS is off: set TLS_CERT_FILE+TLS_KEY_FILE or ACME_DOMAIN, or TLS_DISABLED=true if a reverse proxy terminates TLS")
fi
[ -z "$(env_value METRICS_TOKEN)" ] && problems+=("no dedicated /metrics token: set METRICS_TOKEN (without it /metrics takes the master key)")
master="$(env_value MASTER_API_KEY)"
if [ -n "$master" ] && [ "${#master}" -lt 32 ]; then
  problems+=("MASTER_API_KEY is shorter than 32 characters")
fi
[ -z "$(env_value DOCKER_RUNTIME)" ] && problems+=("sandboxes share the host kernel: install gVisor (make gvisor) and set DOCKER_RUNTIME=runsc, or DOCKER_RUNTIME=runc to accept the shared kernel explicitly")
[ -z "$(env_value AUDIT_HMAC_KEY)" ] && problems+=("audit log entries are unsigned: set AUDIT_HMAC_KEY")
# Compose points the API at its own Redis unless REDIS_ADDR says otherwise.
redis_addr="$(env_value REDIS_ADDR)"
if [ -n "${redis_addr:-redis:6379}" ] && [ -z "$(env_value REDIS_PASSWORD)" ]; then
  problems+=("Redis has no password: set REDIS_PASSWORD")
fi

if [ "${#problems[@]}" -gt 0 ]; then
  {
    echo "Note: VAULTRUN_INSECURE_DEV is not true in $ENV_FILE, so the API will refuse to start:"
    for p in "${problems[@]}"; do echo "  - $p"; done
    echo "Fix these (docs/production-hardening.md), or for local use only run: make dev-env"
  } >&2
fi
exit 0
