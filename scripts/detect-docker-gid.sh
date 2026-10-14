#!/usr/bin/env bash
# Detect the group ID that owns the Docker socket, so the non-root API
# container can talk to the Docker daemon (docker-compose `group_add`).
#
# Usage:
#   scripts/detect-docker-gid.sh            print the GID
#   scripts/detect-docker-gid.sh --write    set DOCKER_GID in .env (creates it if missing)
#   scripts/detect-docker-gid.sh --write path/to/envfile
set -euo pipefail

SOCKET="${DOCKER_SOCKET:-/var/run/docker.sock}"

detect() {
    if [ -S "$SOCKET" ]; then
        stat -c '%g' "$SOCKET" 2>/dev/null || stat -f '%g' "$SOCKET" 2>/dev/null && return 0
    fi
    if getent group docker >/dev/null 2>&1; then
        getent group docker | cut -d: -f3
        return 0
    fi
    return 1
}

GID="$(detect || true)"
if [ -z "$GID" ]; then
    echo "error: could not detect the Docker socket GID (is Docker installed and running?)" >&2
    echo "       check 'ls -ln $SOCKET' and set DOCKER_GID=<gid> in .env manually" >&2
    exit 1
fi

if [ "${1:-}" != "--write" ]; then
    echo "$GID"
    exit 0
fi

ENV_FILE="${2:-.env}"
touch "$ENV_FILE"
if grep -q '^DOCKER_GID=' "$ENV_FILE"; then
    tmp="$(mktemp)"
    sed "s/^DOCKER_GID=.*/DOCKER_GID=$GID/" "$ENV_FILE" > "$tmp"
    cat "$tmp" > "$ENV_FILE"
    rm -f "$tmp"
else
    printf '\nDOCKER_GID=%s\n' "$GID" >> "$ENV_FILE"
fi
echo "DOCKER_GID=$GID written to $ENV_FILE" >&2
