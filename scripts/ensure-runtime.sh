#!/usr/bin/env bash
# Pick gVisor for the sandboxes when the Docker daemon has it.
#
#   DOCKER_RUNTIME empty and runsc registered with Docker: set to runsc.
#   DOCKER_RUNTIME empty and no runsc: leave it, and say how to get gVisor.
#
# A value you set yourself (runsc, runc, ...) is never touched.
# Usage: scripts/ensure-runtime.sh [.env]
set -eu
ENV_FILE="${1:-.env}"
[ -f "$ENV_FILE" ] || exit 0

current=$(grep -E '^[[:space:]]*DOCKER_RUNTIME=' "$ENV_FILE" | tail -n 1 | sed -e 's/^[^=]*=//' -e 's/[[:space:]]*#.*$//' -e 's/["'"'"']//g' || true)
[ -n "$current" ] && exit 0

if docker info --format '{{json .Runtimes}}' 2>/dev/null | grep -q '"runsc"'; then
  if grep -qE '^[[:space:]]*DOCKER_RUNTIME=' "$ENV_FILE"; then
    sed -i.bak -E 's/^[[:space:]]*DOCKER_RUNTIME=.*/DOCKER_RUNTIME=runsc/' "$ENV_FILE" && rm -f "$ENV_FILE.bak"
  else
    printf '\nDOCKER_RUNTIME=runsc\n' >> "$ENV_FILE"
  fi
  echo "gVisor (runsc) found: sandboxes will run under it (DOCKER_RUNTIME=runsc in $ENV_FILE)."
else
  echo "Note: sandboxes share the host kernel. For untrusted or AI-generated code, install gVisor"
  echo "      (make gvisor, Linux) and run make up again; production starts refuse the shared"
  echo "      kernel unless DOCKER_RUNTIME=runc is set explicitly."
fi
