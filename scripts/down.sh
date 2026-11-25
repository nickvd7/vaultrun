#!/usr/bin/env bash
# Stop VaultRun.
#
# Default: stop the compose services only. Sandbox containers, their networks
# and the workspace files stay, so sessions survive `make up` again.
# --all:   also remove sandbox containers whose workspace is under
#          WORKSPACE_HOST_DIR (another stack's sandboxes stay), their
#          per-session networks and egress rules, and the files in that
#          directory. Sessions of this installation are gone afterwards. The database,
#          Redis and certificate volumes are kept.
# --purge: everything --all does, and also delete those volumes: API keys, the
#          audit log, evidence and TLS certificates are gone afterwards.
# Usage: scripts/down.sh [--all|--purge] [.env]
set -u
ALL=0
PURGE=0
case "${1:-}" in
  --all) ALL=1; shift ;;
  --purge) ALL=1; PURGE=1; shift ;;
esac
ENV_FILE="${1:-.env}"
COMPOSE=(docker compose -f deployments/docker-compose.yml --env-file "$ENV_FILE")
if [ -n "${COMPOSE_EXTRA:-}" ]; then
  COMPOSE+=(-f "$COMPOSE_EXTRA")
fi

# Only sandboxes whose workspace mount is under this installation's directory.
# A second stack (the quickstart beside the production profile) keeps its own.
WS=$(sed -n 's/^[[:space:]]*WORKSPACE_HOST_DIR=//p' "$ENV_FILE" 2>/dev/null | tail -n1 | tr -d "\"'")
WS=${WS:-/var/lib/vaultrun/workspaces}
WS=${WS%/}

stack_sandboxes() {
  local id src
  for id in $(docker ps -aq --filter label=vaultrun.managed=true 2>/dev/null); do
    src=$(docker inspect -f '{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}' "$id" 2>/dev/null || true)
    if printf '%s\n' "$src" | grep -q "^${WS}/"; then
      printf '%s\n' "$id"
    fi
  done
}

sandboxes=$(stack_sandboxes)
if [ "$ALL" -eq 0 ]; then
  "${COMPOSE[@]}" down
  if [ -n "$sandboxes" ]; then
    echo "Note: $(echo "$sandboxes" | wc -l | tr -d ' ') sandbox container(s) keep running with their files; sessions resume after 'make up'."
    echo "      'make down-all' removes them (and their sessions and files)."
  fi
  exit 0
fi
sessions=""
if [ -n "$sandboxes" ]; then
  echo "Removing $(printf '%s\n' "$sandboxes" | grep -c .) sandbox container(s) from $WS..."
  for id in $sandboxes; do
    sid=$(docker inspect -f '{{index .Config.Labels "vaultrun.session"}}' "$id" 2>/dev/null || true)
    if [ -n "$sid" ]; then
      sessions="$sessions $sid"
    fi
  done
  # shellcheck disable=SC2086
  docker rm -f $sandboxes >/dev/null
fi

# Per-session egress chains live in the host network namespace (netpolicy helper).
NP=$(sed -n 's/^[[:space:]]*NETPOLICY_CONTAINER=//p' "$ENV_FILE" 2>/dev/null | tail -n1 | tr -d "\"'")
NP=${NP:-vaultrun-netpolicy}
if [ "$(docker inspect -f '{{.State.Running}}' "$NP" 2>/dev/null)" = "true" ]; then
  docker exec "$NP" sh -c '
    for c in $(iptables -S FORWARD 2>/dev/null | sed -n "s/^-A FORWARD -i \([^ ]*\) -j \(vr-[A-Za-z0-9_-]*\)$/\1:\2/p"); do
      iface=${c%%:*}; chain=${c##*:}
      iptables -D FORWARD -i "$iface" -j "$chain"
      ip6tables -D FORWARD -i "$iface" -j DROP 2>/dev/null
      iptables -F "$chain"; iptables -X "$chain"
    done
    for chain in $(iptables -S 2>/dev/null | sed -n "s/^-N \(vr-[A-Za-z0-9_-]*\)$/\1/p"); do
      iptables -F "$chain"; iptables -X "$chain"
    done' >/dev/null 2>&1 || true
fi

for sid in $sessions; do
  nets=$(docker network ls -q --filter label=vaultrun.session="$sid" 2>/dev/null || true)
  if [ -n "$nets" ]; then
    # shellcheck disable=SC2086
    docker network rm $nets >/dev/null 2>&1 || true
  fi
done

if [ "$PURGE" -eq 1 ]; then
  "${COMPOSE[@]}" down --volumes
else
  "${COMPOSE[@]}" down
fi

# Session directories belong to the API user (uid 65532) and are mode 0700, so
# a throwaway root container removes them. The base directory itself is kept.
if docker run --rm -v "$WS:/ws" alpine:3.21 sh -c 'test -n "$(ls -A /ws)"' >/dev/null 2>&1; then
  echo "Removing session files in $WS..."
  docker run --rm -v "$WS:/ws" alpine:3.21 sh -c 'find /ws -mindepth 1 -maxdepth 1 -exec rm -rf {} +' \
    || echo "Warning: could not remove the contents of $WS; remove them manually with sudo."
fi

if [ "$PURGE" -eq 0 ]; then
  echo "Kept the database, Redis and certificate volumes (API keys, audit log, evidence)."
  echo "'make purge' removes those too."
fi
