#!/usr/bin/env bash
# Fail when the user-visible version strings disagree. The dashboard shows
# apps/frontend/package.json, PyPI publishes sdk/python/pyproject.toml and the
# site advertises softwareVersion, and the Go binaries report
# internal/version/VERSION; all four move together on every release.
set -eu
py=$(sed -n 's/^version = "\(.*\)"/\1/p' sdk/python/pyproject.toml | head -1)
fe=$(sed -n 's/^  "version": "\(.*\)",/\1/p' apps/frontend/package.json | head -1)
site=$(sed -n 's/.*"softwareVersion": "\(.*\)".*/\1/p' site/index.html | head -1)
go=$(tr -d '[:space:]' < internal/version/VERSION)
echo "python sdk: $py | dashboard: $fe | site: $site | go: $go"
if [ -z "$py" ] || [ "$py" != "$fe" ] || [ "$py" != "$site" ] || [ "$py" != "$go" ]; then
  echo "Version mismatch: bump sdk/python/pyproject.toml, apps/frontend/package.json (and package-lock.json), site/index.html and internal/version/VERSION together." >&2
  exit 1
fi
