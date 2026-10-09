.PHONY: all build test lint clean up down logs help

BINARY_API   := bin/vaultrun-api
BINARY_CLI   := bin/vaultrun
BINARY_LOCAL := bin/vaultrun-local

GO          := go
MODULE      := github.com/nickvd7/vaultrun

# ── Default ───────────────────────────────────────────────────────────────────
all: build

# ── Build ─────────────────────────────────────────────────────────────────────
build: build-api build-cli build-local

build-api:
	@mkdir -p bin
	$(GO) build -ldflags="-s -w" -o $(BINARY_API) ./cmd/api

build-cli:
	@mkdir -p bin
	$(GO) build -ldflags="-s -w" -o $(BINARY_CLI) ./cmd/cli

build-local:
	@mkdir -p bin
	$(GO) build -ldflags="-s -w" -o $(BINARY_LOCAL) ./cmd/local

# Enterprise features (SSO/SAML) live in the separate vaultrun-enterprise
# repository and are built from there as an overlay on this checkout.
# Commercial licensing: mail@030.dev

# ── Run (local dev) ───────────────────────────────────────────────────────────
run-api: build-api
	$(BINARY_API)

# ── Docker Compose ────────────────────────────────────────────────────────────
up:
	docker compose -f deployments/docker-compose.yml --env-file .env up -d --build

down:
	docker compose -f deployments/docker-compose.yml down

logs:
	docker compose -f deployments/docker-compose.yml logs -f api

ps:
	docker compose -f deployments/docker-compose.yml ps

# ── Tests ─────────────────────────────────────────────────────────────────────
test:
	$(GO) test ./internal/... ./sdk/go/... ./cmd/local/... -v -race -timeout 60s

test-local-gateway:
	./scripts/local-gateway-smoke.sh

test-integration:
	$(GO) test -tags=integration ./tests/integration/... -v -timeout 300s

test-python:
	cd sdk/python && python -m pytest tests/ -v

# ── Code quality ──────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./...

fmt:
	$(GO) fmt ./...
	goimports -w .

vet:
	$(GO) vet ./...

# ── DB helpers ────────────────────────────────────────────────────────────────
migrate-up:
	docker run --rm --network host \
	  -v $(PWD)/migrations:/migrations \
	  migrate/migrate \
	  -path=/migrations -database="$(DATABASE_URL)" up

migrate-down:
	docker run --rm --network host \
	  -v $(PWD)/migrations:/migrations \
	  migrate/migrate \
	  -path=/migrations -database="$(DATABASE_URL)" down 1

# ── Bootstrap ─────────────────────────────────────────────────────────────────
# Creates the first API key using the master key
bootstrap-key:
	@echo "Creating initial API key..."
	curl -s -X POST http://localhost:8080/api/v1/keys \
	  -H "X-API-Key: $${MASTER_API_KEY:-changeme-master-key}" \
	  -H "Content-Type: application/json" \
	  -d '{"name":"default"}' | jq .

# ── Clean ─────────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/

# ── Help ──────────────────────────────────────────────────────────────────────
help:
	@echo "VaultRun Makefile targets:"
	@echo ""
	@echo "  make build           Build API server, CLI, and local gateway"
	@echo "  make build-local     Build OpenAI-compat local inference gateway"
	@echo "  make test-local-gateway  Automated local-gateway smoke (no Ollama)"
	@echo "  make up              Start all services via Docker Compose"
	@echo "  make down            Stop all services"
	@echo "  make logs            Tail API logs"
	@echo "  make test            Run unit tests"
	@echo "  make test-integration Run integration tests (requires running stack)"
	@echo "  make bootstrap-key   Create the first API key"
	@echo "  make migrate-up      Run DB migrations manually"
	@echo "  make clean           Remove build artifacts"
