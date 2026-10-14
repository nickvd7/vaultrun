#!/usr/bin/env bash
# Tell users with an older .env why the API may refuse to start (secure by default since v0.4.0).
# Usage: scripts/check-env.sh [.env]
ENV_FILE="${1:-.env}"
[ -f "$ENV_FILE" ] || exit 0
if grep -Eq '^[[:space:]]*VAULTRUN_INSECURE_DEV=' "$ENV_FILE"; then
  exit 0
fi
cat >&2 <<MSG
Note: $ENV_FILE has no VAULTRUN_INSECURE_DEV. Since v0.4.0 the API refuses to start unless an OPA
policy, TLS and METRICS_TOKEN are configured (docs/production-hardening.md).
  Local use:   make dev-env   (adds VAULTRUN_INSECURE_DEV=true to $ENV_FILE)
  Deployment:  configure OPA_POLICY_FILE, TLS_CERT_FILE/TLS_KEY_FILE (or ACME_DOMAIN), METRICS_TOKEN
MSG
