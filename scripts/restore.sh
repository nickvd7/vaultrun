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
if [ -n "${COMPOSE_EXTRA:-}" ]; then
  COMPOSE+=(-f "$COMPOSE_EXTRA")
fi

if [ -n "$("${COMPOSE[@]}" ps --status running -q api 2>/dev/null)" ]; then
  die "VaultRun is running; stop it first (make down)."
fi
WS=$(sed -n 's/^[[:space:]]*WORKSPACE_HOST_DIR=//p' "$ENV_FILE" | tail -n1 | tr -d "\"'")
WS=${WS:-/var/lib/vaultrun/workspaces}

echo "Starting the database..."
"${COMPOSE[@]}" up -d postgres
# A new data directory makes the Postgres image start a temporary server, shut
# it down, and only then start the real one. The first pg_isready can land in
# that window, and pg_restore then fails with "the database system is shutting
# down". Wait until this container start has logged that the real server is
# accepting connections.
cid=$("${COMPOSE[@]}" ps -q postgres)
started=$(docker inspect -f '{{.State.StartedAt}}' "$cid")
ready=0
for _ in $(seq 1 90); do
  if docker logs --since "$started" "$cid" 2>&1 | awk '
    /The files belonging to this database system will be owned by/ { fresh=1; ready=0 }
    /PostgreSQL init process complete/ { inited=1; ready=0 }
    /database system is ready to accept connections/ { ready=1 }
    END {
      if (fresh) exit (inited && ready) ? 0 : 1
      exit ready ? 0 : 1
    }
  ' && "${COMPOSE[@]}" exec -T postgres pg_isready -U vaultrun -d vaultrun >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[ "$ready" = 1 ] || die "postgres did not become ready"
echo "Restoring the database..."
"${COMPOSE[@]}" exec -T postgres pg_restore -U vaultrun -d vaultrun --clean --if-exists \
  --no-owner --no-privileges --no-comments --single-transaction < "$B/postgres.dump"

echo "Restoring snapshots and artifacts into $WS..."
docker run --rm -i -v "$WS:/ws" alpine:3.21 sh -c 'mkdir -p /ws && tar --numeric-owner -xzf - -C /ws' < "$B/files.tar.gz"

"${COMPOSE[@]}" stop postgres >/dev/null
echo "Restored $(sed -n 1p "$B/VERSION" 2>/dev/null) from $B. Run make up."
