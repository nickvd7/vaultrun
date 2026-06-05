# Changelog

All notable changes to VaultRun are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [0.1.0] — 2026-06-05

First tagged release. Establishes the core platform and the full MCP server feature set.

### Added — Core platform

- **API server** (`cmd/api`) — Gin-based REST API with sessions, command execution, file vault, audit logs, and API key management
- **CLI** (`cmd/cli`) — `vaultrun` command-line tool for session and file management
- **Dashboard** (`apps/frontend`) — Next.js management UI with sessions, run output, file browser, and audit log viewer
- **Go SDK** (`sdk/go`) — typed Go client library
- **Python SDK** (`sdk/python`) — Python client library
- **Docker Compose stack** (`deployments/`) — API + Postgres + Redis + dashboard, ready with `make up`
- **Postgres migrations** (`migrations/`) — managed with golang-migrate
- **OPA policy hook** (`internal/policy/`) — pluggable policy evaluation for request authorization

### Added — MCP server (`sdk/mcp`, 53 tools)

- **Sandbox tools (13)** — create/list/get/delete sessions, run commands, upload/read/list/delete files, get runs, list runs, session stats and logs
- **Docker image tools (2)** — list images, pull image
- **Snapshot tools (2)** — create and list workspace snapshots
- **Artifact & audit tools (3)** — create artifacts, list artifacts, list HMAC-signed audit logs
- **GitHub tools (2)** — clone and run a repo, post PR/issue comments; uses `http.extraheader` so the token never appears in any URL
- **Filesystem tools (4)** — read, write, list, delete — requires explicit `MCP_FS_ALLOWED_PATHS` allowlist; symlink-safe
- **AWS — S3 tools (6)** — list buckets/objects, get/put/delete/head object; requires `MCP_AWS_ENABLED=true` opt-in
- **AWS — SSM Parameter Store tools (4)** — get (with optional decryption), put, delete, list; SecureString values redacted by default
- **AWS — Secrets Manager tools (2)** — get secret (audit-log result redacted), list secret metadata
- **AWS — Lambda tools (2)** — list functions, invoke (6 MB payload cap, heavy rate-limit tier)
- **SQLite tools (3)** — query (SELECT/PRAGMA), execute (INSERT/UPDATE/DELETE/DDL), schema (DDL); requires `MCP_SQLITE_PATH`
- **PostgreSQL tools (3)** — query, execute, schema via `information_schema`; requires `MCP_PG_DSN`
- **MongoDB tools (7)** — find (with filter + limit), insert one, update (one/many), delete (one/many), aggregate (pipeline), list collections, generate Mongoose schema by sampling documents

#### MCP server transports

- **stdio** (default) — JSON-RPC 2.0 over stdin/stdout; compatible with Claude Desktop and Claude Code
- **HTTP** (`MCP_TRANSPORT=http`) — Gin server with `POST /mcp`, `GET /sse`, `GET /`, `GET /healthz`; suitable for OpenAI, OpenRouter, and custom agents

#### MCP server security

- Bearer token authentication (`MCP_AUTH_TOKEN`) — required for HTTP transport; server refuses to start without it
- Per-IP rate limiting: read (60/min), write (30/min), heavy (10/min)
- Three-tier tool classification: normal reads, write mutations, heavy/resource-intensive operations
- Security headers on every HTTP response (`X-Content-Type-Options`, `X-Frame-Options`, etc.)
- CORS configuration via `MCP_ALLOWED_ORIGINS`
- Optional TLS via Let's Encrypt (`MCP_ACME_*`) or static cert (`MCP_TLS_CERT`/`MCP_TLS_KEY`)
- Audit logging for every `tools/call` — sensitive tool results (`sm_get_secret`, `ssm_get_parameter`) are redacted
- `MCP_AWS_ENABLED=true` explicit opt-in prevents ambient IAM credential activation in EC2/ECS environments
- Constant-time token comparison to prevent timing attacks
- `bufio.ReadSlice` loop replaces `bufio.Scanner` — oversized stdio messages return an error without terminating the session
- Input validation: path traversal prevention, positive-only resource limits, GitHub issue number bounds

### Added — GitHub CI Runner (`cmd/ci-runner`)

- Webhook-driven CI: GitHub `pull_request` events (opened/synchronize/reopened) trigger test runs inside VaultRun sandboxes
- HMAC-SHA256 webhook signature validation
- Configurable test commands via `CI_TEST_COMMANDS` (JSON array of command arrays)
- Token-safe git clone via `GIT_CONFIG_KEY_0 = http.https://github.com/.extraheader`
- Results posted as a Markdown PR comment with pass/fail table and collapsible output sections
- GitHub commit status (`vaultrun-ci`) updated to pending → success/failure
- **Slack notifications** — Block Kit payload: header, 4-field metadata section, per-step results, divider, footer
- **Microsoft Teams notifications** — Adaptive Card 1.4 in Workflows webhook envelope; FactSet metadata, step results, "View Pull Request" action button
- `NOTIFY_ON_SUCCESS=false` suppresses notifications on green runs
- Graceful shutdown with 5-minute drain for in-flight CI runs
- `/healthz` endpoint

### Security fixes (applied before 0.1.0 tag)

- **H1** — SSM `get_parameter`: SecureString values no longer returned without explicit `with_decryption=true`
- **H2** — Sensitive tool results (`sm_get_secret`, `ssm_get_parameter`) redacted from MCP audit logs
- **H3** — GitHub token injection switched from URL-embedding to `http.extraheader` in all clone operations
- **M1** — Filesystem tool allowed-paths: symlinks resolved at startup (`filepath.EvalSymlinks`) to prevent TOCTOU bypass
- **M2** — Resource limit parameters (`cpu_limit`, `memory_limit_mb`, `timeout_seconds`) validated to be positive before use
- **M3** — Lambda invoke payload capped at 6 MB to match AWS limit
- **M4** — Stdio session recovery: oversized messages drained and session continues instead of terminating
- **M5** — Per-tool rate-limit tiers applied on HTTP transport for write and heavy operations
- **L3** — `DownloadFile` in client capped at 10 MB to prevent memory exhaustion
- **L4** — GitHub issue number upper-bounded at 100,000,000
- **L5** — `MCP_ALLOWED_ORIGINS` comment clarifies that `*` is only suitable for local development

### Infrastructure

- `.gitignore` — anchored `/mcp` and `sdk/mcp/mcp` build artifact entries; `sdk/mcp/` source directory was previously being ignored
- `Makefile` targets: `build`, `test`, `test-integration`, `test-python`, `lint`, `fmt`, `vet`, `up`, `down`, `migrate-up`, `migrate-down`, `bootstrap-key`
- OpenAPI spec at `docs/openapi.yaml`
- Architecture, security, configuration, and secrets documentation in `docs/`

[0.1.0]: https://github.com/nickvd7/vaultrun/releases/tag/v0.1.0
