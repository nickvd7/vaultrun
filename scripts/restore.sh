#!/usr/bin/env bash
# Restore a backup made by scripts/backup.sh, on the same or a new host.
#
# VaultRun must be stopped (make down). The database is replaced by the one in
# the backup; snapshots and artifacts are put back in the workspace
# directory. Without a .env the backup's is used; an existing .env must match
# it (the audit and evidence keys belong to the database), unless
# RESTORE_KEEP_ENV=1. Afterwards run make up: sessions that were running when
# the backup was made are closed, since their sandboxes are gone.
# Usage: scripts/restore.sh <backup dir> [.env]
set -euo pipefail
B="${1:?usage: scripts/restore.sh <backup dir> [.env]}"
ENV_FILE="${2:-.env}"
die() { echo "restore: $*" >&2; exit 1; }
for f in postgres.dump files.tar.gz env SHA256SUMS; do
  [ -f "$B/$f" ] || die "$B/$f is missing: not a VaultRun backup."
done
(cd "$B" && sha256sum -c --quiet SHA256SUMS) || die "checksums do not match: the backup is damaged."

if [ ! -f "$ENV_FILE" ]; then
  (umask 077 && cp "$B/env" "$ENV_FILE")
  echo "Restored $ENV_FILE from the backup."
elif ! cmp -s "$B/env" "$ENV_FILE" && [ "${RESTORE_KEEP_ENV:-}" != "1" ]; then
  die "$ENV_FILE differs from the backup's. Its keys (AUDIT_HMAC_KEY, EVIDENCE_SIGNING_KEY, POSTGRES_PASSWORD, ...) must match the database: move $ENV_FILE away to use the backup's, or set RESTORE_KEEP_ENV=1 to keep yours."
fi
COMPOSE=(docker compose -f deployments/docker-compose.yml --env-file "$ENV_FILE")

if [ -n "$("${COMPOSE[@]}" ps --status running -q api 2>/dev/null)" ]; then
  die "VaultRun is running; stop it first (make down)."
fi
WS=$(sed -n 's/^[[:space:]]*WORKSPACE_HOST_DIR=//p' "$ENV_FILE" | tail -n1 | tr -d "\"'")
WS=${WS:-/var/lib/vaultrun/workspaces}

echo "Starting the database..."
"${COMPOSE[@]}" up -d postgres
for _ in $(seq 1 60); do
  "${COMPOSE[@]}" exec -T postgres pg_isready -U vaultrun -d vaultrun >/dev/null 2>&1 && break
  sleep 1
done
echo "Restoring the database..."
"${COMPOSE[@]}" exec -T postgres pg_restore -U vaultrun -d vaultrun --clean --if-exists \
  --no-owner --no-privileges --no-comments --single-transaction < "$B/postgres.dump"

echo "Restoring snapshots and artifacts into $WS..."
docker run --rm -i -v "$WS:/ws" alpine:3.21 sh -c 'mkdir -p /ws && tar --numeric-owner -xzf - -C /ws' < "$B/files.tar.gz"

"${COMPOSE[@]}" stop postgres >/dev/null
echo "Restored $(sed -n 1p "$B/VERSION" 2>/dev/null) from $B. Run make up."
