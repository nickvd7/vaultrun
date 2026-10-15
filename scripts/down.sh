#!/usr/bin/env bash
# Stop VaultRun.
#
# Default: stop the compose services only. Sandbox containers, their networks
# and the workspace files stay, so sessions survive `make up` again.
# --all:   also remove every sandbox container, per-session network and egress
#          rule the API created. Sessions and their files are gone afterwards
#          (the next `make up` closes them and removes their directories).
# Usage: scripts/down.sh [--all] [.env]
set -u
ALL=0
if [ "${1:-}" = "--all" ]; then ALL=1; shift; fi
ENV_FILE="${1:-.env}"
COMPOSE=(docker compose -f deployments/docker-compose.yml --env-file "$ENV_FILE")

sandboxes=$(docker ps -aq --filter label=vaultrun.managed=true)
if [ "$ALL" -eq 0 ]; then
  "${COMPOSE[@]}" down
  if [ -n "$sandboxes" ]; then
    echo "Note: $(echo "$sandboxes" | wc -l | tr -d ' ') sandbox container(s) keep running with their files; sessions resume after 'make up'."
    echo "      'make down-all' removes them (and their sessions and files)."
  fi
  exit 0
fi
if [ -n "$sandboxes" ]; then
  echo "Removing $(echo "$sandboxes" | wc -l | tr -d ' ') sandbox container(s)..."
  # shellcheck disable=SC2086
  docker rm -f $sandboxes >/dev/null
fi

# Per-session egress chains live in the host network namespace (netpolicy helper).
if [ "$(docker inspect -f '{{.State.Running}}' vaultrun-netpolicy 2>/dev/null)" = "true" ]; then
  docker exec vaultrun-netpolicy sh -c '
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

nets=$(docker network ls -q --filter label=vaultrun.managed=true)
if [ -n "$nets" ]; then
  # shellcheck disable=SC2086
  docker network rm $nets >/dev/null 2>&1 || true
fi

"${COMPOSE[@]}" down
