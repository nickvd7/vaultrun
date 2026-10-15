#!/usr/bin/env bash
# Generate AUDIT_HMAC_KEY in .env when it is missing or empty, so that audit
# entries are signed out of the box. An existing value is never touched:
# changing the key invalidates earlier signatures.
# Usage: scripts/ensure-audit-key.sh [.env]
set -eu
ENV_FILE="${1:-.env}"
[ -f "$ENV_FILE" ] || exit 0
if grep -Eq '^[[:space:]]*AUDIT_HMAC_KEY=.+' "$ENV_FILE"; then
  exit 0
fi
if command -v openssl >/dev/null 2>&1; then
  key=$(openssl rand -hex 32)
else
  key=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
fi
tmp=$(mktemp)
if grep -Eq '^[[:space:]]*AUDIT_HMAC_KEY=' "$ENV_FILE"; then
  awk -v k="$key" '/^[[:space:]]*AUDIT_HMAC_KEY=/ { print "AUDIT_HMAC_KEY=" k; next } { print }' "$ENV_FILE" > "$tmp"
else
  { cat "$ENV_FILE"; printf '\nAUDIT_HMAC_KEY=%s\n' "$key"; } > "$tmp"
fi
cat "$tmp" > "$ENV_FILE"
rm -f "$tmp"
echo "Generated AUDIT_HMAC_KEY in $ENV_FILE (audit entries are now signed; keep this value, changing it invalidates old signatures)."
