#!/usr/bin/env bash
# Install gVisor (runsc) and register it with Docker, so sandboxes can run
# under a user-space kernel (DOCKER_RUNTIME=runsc).
#
# Linux only; needs sudo. Installs a pinned release after checking its
# SHA-512, the same one CI tests against. Override with GVISOR_RELEASE.
# Usage: scripts/install-gvisor.sh
set -euo pipefail
RELEASE="${GVISOR_RELEASE:-20261019.0}"

if [ "$(uname -s)" != "Linux" ]; then
  echo "gVisor runs on Linux only. On macOS or Windows, run VaultRun on a Linux host or VM." >&2
  exit 1
fi
case "$(uname -m)" in
  x86_64|aarch64) ARCH=$(uname -m) ;;
  *) echo "gVisor has no release for $(uname -m)." >&2; exit 1 ;;
esac

URL="https://storage.googleapis.com/gvisor/releases/release/${RELEASE}/${ARCH}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
echo "Downloading gVisor ${RELEASE} (${ARCH})..."
curl -fsSL -o "$TMP/gvisor.tar.bz2" "$URL/gvisor.tar.bz2"
curl -fsSL -o "$TMP/gvisor.tar.bz2.sha512" "$URL/gvisor.tar.bz2.sha512"
(cd "$TMP" && sha512sum -c gvisor.tar.bz2.sha512)

sudo tar xjf "$TMP/gvisor.tar.bz2" -C /usr/local/bin
sudo /usr/local/bin/runsc install
# Runtimes are reloadable: no restart, running containers keep running.
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet docker 2>/dev/null; then
  sudo systemctl reload docker
else
  sudo pkill -HUP -x dockerd
fi
for _ in 1 2 3 4 5; do
  if docker info --format '{{json .Runtimes}}' 2>/dev/null | grep -q '"runsc"'; then
    echo "gVisor installed and registered with Docker. Run make up: sandboxes will use it."
    exit 0
  fi
  sleep 1
done
echo "runsc is installed but Docker does not list it; restart Docker and check docker info." >&2
exit 1
