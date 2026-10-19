#!/usr/bin/env bash
# Replace placeholder secrets in .env with generated ones, so a quickstart
# (cp .env.example .env; make up) does not run Postgres on the example password
# or Redis without one.
#
#   POSTGRES_PASSWORD  empty or "change-me...": generated, unless Postgres already
#                      has a data volume, because the database keeps the password
#                      it was first started with and a new value would lock the
#                      API out. A warning is printed instead.
#   REDIS_PASSWORD     empty: generated, when Redis is the one Compose starts
#                      (REDIS_ADDR unset or redis:6379). Redis takes the password
#                      from its command line, so changing it is always safe.
#
# Values you chose yourself are never touched.
# Usage: scripts/ensure-secrets.sh [.env]
set -eu
ENV_FILE="${1:-.env}"
[ -f "$ENV_FILE" ] || exit 0

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

gen() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# set_var KEY VALUE: replace the assignment, or append one.
set_var() {
  local tmp
  tmp=$(mktemp)
  if grep -Eq "^[[:space:]]*$1=" "$ENV_FILE"; then
    awk -v k="$1" -v v="$2" '$0 ~ "^[[:space:]]*" k "=" { print k "=" v; next } { print }' "$ENV_FILE" > "$tmp"
  else
    { cat "$ENV_FILE"; printf '\n%s=%s\n' "$1" "$2"; } > "$tmp"
  fi
  cat "$tmp" > "$ENV_FILE"
  rm -f "$tmp"
}

has_volume() {
  command -v docker >/dev/null 2>&1 || return 1
  docker volume ls -q 2>/dev/null | grep -Eq "(^|_)$1\$"
}

pg=$(env_value POSTGRES_PASSWORD)
case "$pg" in
  ""|change-me*)
    if has_volume postgres_data; then
      echo "Warning: POSTGRES_PASSWORD in $ENV_FILE is still the example value, but the database already exists with it." >&2
      echo "         Change it in Postgres first (ALTER USER vaultrun PASSWORD '...'), then in $ENV_FILE." >&2
    else
      new=$(gen)
      set_var POSTGRES_PASSWORD "$new"
      if [ -n "$pg" ]; then
        # DATABASE_URL (used when the API runs outside Compose) carries the same example password.
        tmp=$(mktemp)
        awk -v old="$pg" -v new="$new" '
          /^[[:space:]]*DATABASE_URL=/ {
            out = ""
            while ((i = index($0, old)) > 0) { out = out substr($0, 1, i - 1) new; $0 = substr($0, i + length(old)) }
            $0 = out $0
          }
          { print }' "$ENV_FILE" > "$tmp"
        cat "$tmp" > "$ENV_FILE"
        rm -f "$tmp"
      fi
      echo "Generated POSTGRES_PASSWORD in $ENV_FILE (the example password is not used)."
    fi
    ;;
esac

redis_addr=$(env_value REDIS_ADDR)
if [ -z "$(env_value REDIS_PASSWORD)" ] && { [ -z "$redis_addr" ] || [ "$redis_addr" = "redis:6379" ]; }; then
  set_var REDIS_PASSWORD "$(gen)"
  echo "Generated REDIS_PASSWORD in $ENV_FILE (Redis now requires a password)."
fi
