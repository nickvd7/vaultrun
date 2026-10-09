#!/usr/bin/env bash
# Automated local-gateway smoke (no Ollama / VaultRun stack required).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
echo "==> local-gateway unit + automated smoke"
go test ./internal/localgateway/ -count=1 -race -timeout 120s
echo "==> build vaultrun-local"
go build -o bin/vaultrun-local ./cmd/local
echo "OK: local gateway smoke passed"
