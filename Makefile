.PHONY: all build test lint clean up dev-env down down-all purge gvisor xfs-workspace harden backup restore logs help

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
# ENV_FILE is .env for the quickstart. make prod-up uses .env.production.
ENV_FILE ?= .env
COMPOSE_EXTRA ?=

up:
	@if [ ! -f $(ENV_FILE) ]; then echo "Error: $(ENV_FILE) not found. Run: cp .env.example .env" >&2; exit 1; fi
	@./scripts/ensure-audit-key.sh $(ENV_FILE)
	@./scripts/ensure-secrets.sh $(ENV_FILE)
	@./scripts/ensure-runtime.sh $(ENV_FILE)
	@./scripts/check-env.sh $(ENV_FILE)
	@COMPOSE_EXTRA="$(COMPOSE_EXTRA)" docker compose -f deployments/docker-compose.yml $(if $(COMPOSE_EXTRA),-f $(COMPOSE_EXTRA),) --env-file $(ENV_FILE) up -d --build; rc=$$?; \
	  if [ $$rc -eq 0 ]; then COMPOSE_EXTRA="$(COMPOSE_EXTRA)" ./scripts/wait-healthy.sh $(ENV_FILE) 90 || exit 1; else COMPOSE_EXTRA="$(COMPOSE_EXTRA)" ./scripts/wait-healthy.sh $(ENV_FILE) 15 compose-failed || true; fi; exit $$rc

# Production profile: its own ports, project and workspace, so it can run
# beside a quickstart on 3000/8080. Does not install gVisor or format a disk.
prod-env:
	@./scripts/prod-env.sh

prod-up: prod-env
	@$(MAKE) up ENV_FILE=.env.production COMPOSE_EXTRA=deployments/docker-compose.prod.yml

prod-down:
	@$(MAKE) down ENV_FILE=.env.production COMPOSE_EXTRA=deployments/docker-compose.prod.yml

# Reload Caddy only. --no-deps is required: a plain `up caddy` follows
# depends_on and recreates the API and the frontend. That build has no SSO,
# so a stack with OIDC_* or SAML_* set then exits.
prod-caddy:
	@if [ ! -f .env.production ]; then echo "Error: .env.production not found. Run: make prod-env" >&2; exit 1; fi
	@docker compose -f deployments/docker-compose.yml -f deployments/docker-compose.prod.yml --env-file .env.production up -d --no-build --no-deps caddy

prod-logs:
	@$(MAKE) logs ENV_FILE=.env.production COMPOSE_EXTRA=deployments/docker-compose.prod.yml

# Local development only: opt out of the secure-by-default startup checks.
dev-env:
	@if [ ! -f .env ]; then echo "Error: .env not found. Run: cp .env.example .env" >&2; exit 1; fi
	@if grep -Eq '^[[:space:]]*VAULTRUN_INSECURE_DEV=(true|1|TRUE|True)[[:space:]]*$$' .env; then echo "VAULTRUN_INSECURE_DEV=true already set in .env"; \
	elif grep -Eq '^[[:space:]]*VAULTRUN_INSECURE_DEV=' .env; then \
	  sed -i.bak -E 's/^[[:space:]]*VAULTRUN_INSECURE_DEV=.*/VAULTRUN_INSECURE_DEV=true/' .env && rm -f .env.bak && echo "Set VAULTRUN_INSECURE_DEV=true in .env (local development only)"; \
	else echo "VAULTRUN_INSECURE_DEV=true" >> .env && echo "Added VAULTRUN_INSECURE_DEV=true to .env (local development only)"; fi

down:
	@COMPOSE_EXTRA="$(COMPOSE_EXTRA)" ./scripts/down.sh $(ENV_FILE)

# Removes this installation's sandboxes, their networks and egress rules, and
# the files in its workspace. Another stack's sandboxes stay.
down-all:
	@COMPOSE_EXTRA="$(COMPOSE_EXTRA)" ./scripts/down.sh --all $(ENV_FILE)

purge:
	@COMPOSE_EXTRA="$(COMPOSE_EXTRA)" ./scripts/down.sh --purge $(ENV_FILE)

gvisor:
	@./scripts/install-gvisor.sh

# Database, snapshots, artifacts and .env, to BACKUP_DIR (default backups/).
backup:
	@COMPOSE_EXTRA="$(COMPOSE_EXTRA)" ./scripts/backup.sh $(ENV_FILE)

# make restore BACKUP=backups/<time>: with VaultRun stopped, on this or a new host.
restore:
	@if [ -z "$(BACKUP)" ]; then echo "Usage: make restore BACKUP=backups/<time>" >&2; exit 1; fi
	@COMPOSE_EXTRA="$(COMPOSE_EXTRA)" ./scripts/restore.sh "$(BACKUP)" $(ENV_FILE)

# Hard per-session workspace limit: workspaces on XFS with project quotas.
# XFS_DEVICE=/dev/sdX formats that disk; otherwise an image file of XFS_SIZE (50G).
xfs-workspace:
	@./scripts/setup-xfs-workspace.sh .env

# Production host setup: gVisor and the hard workspace quota, then make up.
harden: gvisor xfs-workspace
	@./scripts/ensure-runtime.sh .env
	@echo "Host hardened. Run make up to (re)start VaultRun with gVisor and the workspace quota."

logs:
	COMPOSE_EXTRA="$(COMPOSE_EXTRA)" docker compose -f deployments/docker-compose.yml $(if $(COMPOSE_EXTRA),-f $(COMPOSE_EXTRA),) --env-file $(ENV_FILE) logs -f api

ps:
	docker compose -f deployments/docker-compose.yml $(if $(COMPOSE_EXTRA),-f $(COMPOSE_EXTRA),) --env-file $(ENV_FILE) ps

# ── Tests ─────────────────────────────────────────────────────────────────────
test:
	$(GO) test ./internal/... ./sdk/go/... ./cmd/local/... ./cmd/cli/... -v -race -timeout 60s

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
# Creates the first API key using the master key.
# ENV_FILE=.env.production talks to the production profile (https, its own port).
bootstrap-key:
	@if [ ! -f $(ENV_FILE) ]; then \
	  echo "Error: $(ENV_FILE) not found. Run: cp .env.example .env   or: make prod-env"; \
	  exit 1; \
	fi
	@if ! command -v jq >/dev/null 2>&1; then \
	  echo "Error: jq is required. Install it with: apt-get install jq (Debian/Ubuntu) or brew install jq (macOS)"; \
	  exit 1; \
	fi
	@set -a && . ./$(ENV_FILE) && set +a && \
	if [ -z "$$MASTER_API_KEY" ]; then \
	  echo "Error: MASTER_API_KEY is empty in $(ENV_FILE). Set it to a strong secret (e.g. openssl rand -hex 32)"; \
	  exit 1; \
	fi && \
	scheme=http; curl_tls=; \
	if [ -n "$$TLS_CERT_FILE" ] && [ "$$TLS_DISABLED" != "true" ] && [ "$$TLS_DISABLED" != "1" ]; then \
	  scheme=https; curl_tls=-k; \
	fi && \
	port=$${PORT:-8080} && \
	echo "Creating initial API key on $$scheme://127.0.0.1:$$port ..." >&2 && \
	curl -sS $$curl_tls -X POST "$$scheme://127.0.0.1:$$port/api/v1/keys" \
	  -H "X-API-Key: $${MASTER_API_KEY}" \
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
	@echo "  make prod-env        Write .env.production (checks on, own ports, not the quickstart)"
	@echo "  make prod-up         Start that profile beside a stack already on ports 3000 and 8080"
	@echo "  make prod-down       Stop the production profile"
	@echo "  make prod-caddy      Reload Caddy only; does not recreate the API or the frontend"
	@echo "  make dev-env         Add VAULTRUN_INSECURE_DEV=true to .env (local development only)"
	@echo "  make down            Stop the stack; sandboxes and their files keep running"
	@echo "  make down-all        Stop this stack and remove its sandboxes (another stack's sandboxes stay)"
	@echo "  make purge           down-all, and also delete the database, Redis and certificate volumes"
	@echo "  make gvisor          Install gVisor (Linux, sudo) so sandboxes get a user-space kernel"
	@echo "  make xfs-workspace   Put workspaces on XFS with project quotas (hard per-session limit; sudo)"
	@echo "  make harden          make gvisor + make xfs-workspace: production host setup"
	@echo "  make backup          Back up the database, snapshots, artifacts and .env (BACKUP_DIR)"
	@echo "  make restore BACKUP=dir  Restore a backup (VaultRun stopped; this or a new host)"
	@echo "  make logs            Tail API logs"
	@echo "  make test            Run unit tests"
	@echo "  make test-integration Run integration tests (requires running stack)"
	@echo "  make bootstrap-key   Create the first API key"
	@echo "  make migrate-up      Run DB migrations manually"
	@echo "  make clean           Remove build artifacts"
