#!/usr/bin/env bash
# Stop VaultRun including the sandbox containers the API started.
# `docker compose down` only knows the compose services; sandboxes and their
# per-session networks (and egress rules) are created by the API and would be
# left running with nothing controlling them.
# Usage: scripts/down.sh [.env]
set -u
ENV_FILE="${1:-.env}"
COMPOSE=(docker compose -f deployments/docker-compose.yml --env-file "$ENV_FILE")

sandboxes=$(docker ps -aq --filter label=vaultrun.managed=true)
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
