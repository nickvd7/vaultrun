#!/usr/bin/env bash
# Fail when the user-visible version strings disagree. The dashboard shows
# apps/frontend/package.json, PyPI publishes sdk/python/pyproject.toml and the
# site advertises softwareVersion; all three move together on every release.
set -eu
py=$(sed -n 's/^version = "\(.*\)"/\1/p' sdk/python/pyproject.toml | head -1)
fe=$(sed -n 's/^  "version": "\(.*\)",/\1/p' apps/frontend/package.json | head -1)
site=$(sed -n 's/.*"softwareVersion": "\(.*\)".*/\1/p' site/index.html | head -1)
echo "python sdk: $py | dashboard: $fe | site: $site"
if [ -z "$py" ] || [ "$py" != "$fe" ] || [ "$py" != "$site" ]; then
  echo "Version mismatch: bump sdk/python/pyproject.toml, apps/frontend/package.json (and package-lock.json) and site/index.html together." >&2
  exit 1
fi
