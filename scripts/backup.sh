#!/usr/bin/env bash
# Back up a running VaultRun (Docker Compose) installation.
#
# Writes BACKUP_DIR/<UTC time>/ (default backups/) with:
#   postgres.dump   the database: sessions, runs, API keys, orgs, audit log,
#                   evidence records (pg_dump custom format, consistent)
#   files.tar.gz    snapshots/ and artifacts/ from the workspace directory
#   env             the .env file: the master, audit, evidence and database
#                   keys the dump only works with
#   SHA256SUMS
# Live session workspaces are not included: a sandbox does not survive the
# loss of its host, so restored sessions come back closed. Snapshot what you
# want to keep. The backup holds every secret of the installation: keep it
# encrypted and off this host.
# Usage: scripts/backup.sh [.env]
set -euo pipefail
ENV_FILE="${1:-.env}"
[ -f "$ENV_FILE" ] || { echo "backup: $ENV_FILE not found" >&2; exit 1; }
COMPOSE=(docker compose -f deployments/docker-compose.yml --env-file "$ENV_FILE")

if [ -z "$("${COMPOSE[@]}" ps --status running -q postgres 2>/dev/null)" ]; then
  echo "backup: the postgres service is not running; start VaultRun (make up) first." >&2
  exit 1
fi
WS=$(sed -n 's/^[[:space:]]*WORKSPACE_HOST_DIR=//p' "$ENV_FILE" | tail -n1 | tr -d "\"'")
WS=${WS:-/var/lib/vaultrun/workspaces}

umask 077
DEST="${BACKUP_DIR:-backups}/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$DEST"

echo "Dumping the database..."
"${COMPOSE[@]}" exec -T postgres pg_dump -U vaultrun -d vaultrun -Fc > "$DEST/postgres.dump"

# snapshots/ and artifacts/ belong to the API user (mode 0700): read them
# through a throwaway root container rather than sudo.
echo "Archiving snapshots and artifacts from $WS..."
docker run --rm -v "$WS:/ws:ro" alpine:3.21 sh -c \
  'cd /ws && set -- && for d in snapshots artifacts; do [ -d "$d" ] && set -- "$@" "$d"; done; tar --numeric-owner -czf - "$@"' \
  > "$DEST/files.tar.gz"

cp "$ENV_FILE" "$DEST/env"
version=$("${COMPOSE[@]}" exec -T api /app/api version 2>/dev/null || echo unknown)
printf '%s\ncreated %s\n' "$version" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/VERSION"
(cd "$DEST" && sha256sum postgres.dump files.tar.gz env VERSION > SHA256SUMS)
echo "Backup written to $DEST ($(du -sh "$DEST" | cut -f1))."
echo "It contains every secret of this installation: encrypt it and keep it off this host."
