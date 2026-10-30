#!/usr/bin/env bash
# Put the session workspaces on XFS with project quotas and turn on the hard
# per-session limit (WORKSPACE_QUOTA=xfs in .env).
#
#   XFS_DEVICE=/dev/sdb  format this disk or partition (everything on it is lost)
#   otherwise            a preallocated image file, XFS_IMAGE (default
#                        <WORKSPACE_HOST_DIR>.xfs) of XFS_SIZE (default 50G),
#                        mounted over a loop device
#
# The mount goes into /etc/fstab (prjquota, before Docker starts). The
# workspace directory must be empty, or already be an XFS mount with project
# quotas, in which case only .env is updated. Linux only; needs sudo.
# Usage: scripts/setup-xfs-workspace.sh [.env]
set -euo pipefail
ENV_FILE="${1:-.env}"

die() { echo "setup-xfs-workspace: $*" >&2; exit 1; }
[ "$(uname -s)" = "Linux" ] || die "XFS project quotas need a Linux host."
[ -f "$ENV_FILE" ] || die "$ENV_FILE not found. Run: cp .env.example .env"
command -v mkfs.xfs >/dev/null || die "mkfs.xfs not found: install xfsprogs (apt install xfsprogs / dnf install xfsprogs)."

env_value() {
  local line
  line=$(grep -E "^[[:space:]]*$1=" "$ENV_FILE" | tail -n 1) || true
  line="${line#*=}"; line="${line%%[[:space:]]#*}"; line="${line%$'\r'}"
  line="${line#\"}"; line="${line%\"}"; line="${line#\'}"; line="${line%\'}"
  printf '%s' "$line"
}
set_env() {
  if grep -qE "^[[:space:]]*$1=" "$ENV_FILE"; then
    sed -i.bak -E "s|^[[:space:]]*$1=.*|$1=$2|" "$ENV_FILE" && rm -f "$ENV_FILE.bak"
  else
    printf '\n%s=%s\n' "$1" "$2" >> "$ENV_FILE"
  fi
}

WS="$(env_value WORKSPACE_HOST_DIR)"; WS="${WS:-/var/lib/vaultrun/workspaces}"
case "$WS" in /*) ;; *) die "WORKSPACE_HOST_DIR must be an absolute path (is $WS)." ;; esac
WS="${WS%/}"

has_prjquota() {
  [ "$(findmnt -n -o FSTYPE --target "$1" 2>/dev/null)" = "xfs" ] &&
    findmnt -n -o OPTIONS --target "$1" | tr ',' '\n' | grep -Eq '^(prjquota|pquota|prjquota=.*)$'
}

if findmnt -n --mountpoint "$WS" >/dev/null 2>&1; then
  has_prjquota "$WS" || die "$WS is a mount point but not XFS with prjquota; fix its mount options first."
  echo "$WS is already XFS with project quotas."
else
  sudo mkdir -p "$WS"
  if [ -n "$(sudo ls -A "$WS")" ]; then
    die "$WS is not empty. Stop VaultRun (make down-all) and move or delete the old workspaces first."
  fi
  if [ -n "${XFS_DEVICE:-}" ]; then
    [ -b "$XFS_DEVICE" ] || die "$XFS_DEVICE is not a block device."
    if findmnt -n --source "$XFS_DEVICE" >/dev/null 2>&1; then
      die "$XFS_DEVICE is mounted; unmount it first."
    fi
    echo "Formatting $XFS_DEVICE as XFS: everything on it is lost."
    sudo mkfs.xfs -f -q "$XFS_DEVICE"
    dev="$XFS_DEVICE"
    source="UUID=$(sudo blkid -s UUID -o value "$XFS_DEVICE")"
    opts="defaults,prjquota"
  else
    img="${XFS_IMAGE:-$WS.xfs}"
    size="${XFS_SIZE:-50G}"
    [ -e "$img" ] && die "$img exists already; remove it or set XFS_IMAGE."
    sudo mkdir -p "$(dirname "$img")"
    # Allocated up front, so the workspaces cannot run the host disk full.
    echo "Creating $img ($size)..."
    sudo fallocate -l "$size" "$img"
    sudo chmod 600 "$img"
    sudo mkfs.xfs -q "$img"
    dev="$img"
    source="$img"
    opts="loop,prjquota"
  fi
  if ! sudo mount -t xfs -o "$opts" "$dev" "$WS" || ! has_prjquota "$WS"; then
    findmnt -n --mountpoint "$WS" >/dev/null 2>&1 && sudo umount "$WS"
    [ -n "${img:-}" ] && sudo rm -f "$img"
    die "could not mount $WS as XFS with project quotas: the kernel may lack XFS quota support (CONFIG_XFS_QUOTA). Nothing was changed in /etc/fstab or $ENV_FILE."
  fi
  # Mounted before Docker starts at boot, so containers never see the bare
  # directory; nofail keeps a missing disk from blocking boot (the API then
  # refuses to start, because the directory has no project quotas).
  if ! grep -qsE "^[^#]*[[:space:]]$WS[[:space:]]" /etc/fstab; then
    echo "$source $WS xfs $opts,nofail,x-systemd.before=docker.service 0 0" | sudo tee -a /etc/fstab >/dev/null
  fi
  command -v systemctl >/dev/null 2>&1 && sudo systemctl daemon-reload 2>/dev/null || true
fi

# The API runs as uid 65532 (see workspace-init in docker-compose.yml).
sudo chown 65532:65532 "$WS"
sudo chmod 700 "$WS"

set_env WORKSPACE_QUOTA xfs
max="$(env_value MAX_WORKSPACE_MB)"
if [ -z "$max" ] || [ "$max" = "0" ]; then
  set_env MAX_WORKSPACE_MB 2048
  max=2048
fi
echo "Workspaces on XFS with project quotas: each session is limited to ${max} MB (WORKSPACE_QUOTA=xfs in $ENV_FILE)."
echo "Run make up (again) to apply it to new sessions."
