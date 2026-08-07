# Changelog

All notable changes to VaultRun are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

### Changed

- **MCP protocol dual-support for `2026-07-28`** (`sdk/mcp`) — Stateless
  Streamable HTTP: `server/discover`, per-request `_meta` protocol version,
  `MCP-Protocol-Version` / `Mcp-Method` / `Mcp-Name` header validation, cache
  hints (`ttlMs`, `cacheScope`) on `tools/list` and discover, and `resultType`
  on tool results. Legacy `2024-11-05` `initialize` / `initialized` clients
  keep working without the new headers.

---

## [0.3.2] — 2026-07-31

**Cross-feature security release.** A second testing pass, focused on where features interact rather than each in isolation, found twelve more defects — eight security-relevant. Full analysis in [docs/security-testing-report.md](docs/security-testing-report.md#round-two--cross-feature-and-edge-case-testing).

The most significant finding: the SSRF and Python-escaping fixes shipped in 0.3.1 hardened `internal/browser`, but nothing in the API constructs that package — the MCP server's `browser_*` tools, which is how an agent actually drives the browser, had their own separate and still-vulnerable implementation. That gap is now closed by sharing one implementation between both.

### Fixed — Security

- **MCP browser tools had incomplete escaping and no SSRF check**
  (`sdk/mcp/browser.go`) — `escapeString` only applied JSON escaping, leaving
  single quotes unescaped inside the single-quoted Python literals the
  generated scripts use; a selector like `'); import os; os.system('id')`
  ran as Python inside the sandbox. There was also no URL validation at all
  on this path, and screenshot/PDF paths were not confined to `/workspace`.
  `internal/browser`'s hardening is now exported (`internal/browser/validate.go`)
  and shared by both the (unused) API package and the MCP tools.
- **Checkpoint fork bypassed the session quota** (`internal/replay`) — Forks
  were attributed to the source session's owner, not the caller, so an actor
  at their session limit could keep forking a colleague's session
  indefinitely without it counting against their own quota.
- **Checkpoint fork did not re-check the image allowlist** (`cmd/api/handlers/replay.go`) —
  A fork re-ran the source image without validating it against the current
  allowlist, letting a withdrawn (e.g. newly-vulnerable) image back into
  service via any old session that had used it.
- **`SendMessage` allowed agent impersonation** (`cmd/api/handlers/collab.go`) —
  The `from` field was trusted from the request body with no check that the
  caller was that agent, letting any session member post messages other
  agents would act on as if from a different sender.
- **`SendMessage` required only viewer access** (`cmd/api/handlers/collab.go`) —
  Sending a message is a write into the channel agents act on; raised to
  require `executor`.
- **Agent slot claim was a check-then-act race** (`internal/collab/manager.go`) —
  Counting active agents and then adding one let concurrent joins all pass
  the cap check, so `max_agents` did not hold under concurrent load. Replaced
  with an atomic Lua script.
- **Cost aggregates unscoped by tenant** (`cmd/api/handlers/costs.go`) — The
  breakdown and alert-listing endpoints queried every session in the
  deployment regardless of caller. Now scoped: only the master key sees
  deployment-wide data.
- **Template update/delete had no ownership check** (`internal/templates/manager.go`) —
  Any authenticated key could retarget any template, including built-ins, at
  an arbitrary image. Now requires admin of the authoring org; built-ins are
  master-key only.
- **NL policy refusals answered 500 instead of 422** (`cmd/api/handlers/nlpolicy.go`) —
  A policy the compiler refused for safety (e.g. a prompt-injected
  explanation) was indistinguishable from a server outage. Now 422, and
  `ParsePolicy` validates before returning.

### Fixed — Correctness

- **Template session creation skipped session-creation gates**
  (`cmd/api/handlers/templates.go`) — Creating a session from a template
  bypassed the image allowlist, resource ceilings and per-actor quota that
  `POST /sessions` enforces directly. Now runs the same checks.
- **`GetOrgSummary`, `CostBreakdown`, budget lookup all broken**
  (`internal/cost`) — `FROM orgs` (table is `organizations`), missing `db:`
  tags, and a double-pointer scan target respectively. All three failed on
  every call.
- **`nil` string array serialised as SQL `NULL`** (`internal/models`) —
  `StringArray.Value()` returned `NULL` for a `nil` slice, violating the
  `NOT NULL DEFAULT '{}'` constraint on `sessions.allowed_hosts` and
  `runs.args`. Broke session creation from a template and command runs with
  no arguments.

### Testing

- Added `cmd/api/handlers/features_edge_e2e_test.go`,
  `cmd/api/handlers/collab_e2e_test.go`, `cmd/api/handlers/nlpolicy_e2e_test.go`,
  `sdk/mcp/browser_test.go` — cost/template ownership and parity, replay fork
  attribution and allowlist re-checks, collaboration concurrency and
  impersonation, NL policy input bounds and injection resistance, and browser
  MCP tool SSRF/escaping/path-confinement, run against live PostgreSQL and
  Redis.
- Full suite (`go test ./...`, `-race`, and the integration suite against
  live Postgres + Redis) passes.

## [0.3.1] — 2026-07-31

**Security and correctness release.** Testing the v0.3.0 features against a real
PostgreSQL instance, and writing per-attack-class unit tests, surfaced eleven
defects. Full analysis in [docs/security-testing-report.md](docs/security-testing-report.md).

Upgrade is recommended for every v0.3.0 deployment: two of the six features did
not work at all, and six of the security defects were exploitable with an
ordinary API key.

### Fixed — Security

- **Rego injection via policy explanation** (`internal/nlpolicy`) — The
  LLM-produced explanation was written into a Rego comment unescaped. A newline
  ended the comment and the remainder was parsed as policy code, so an
  explanation containing `allow { true }` turned a restrictive policy into
  allow-all.
- **iptables injection via allowed hosts** (`internal/nlpolicy`) — Hosts were
  interpolated into `iptables -A OUTPUT -d %s -j ACCEPT`. A value such as
  `1.1.1.1 -j ACCEPT; iptables -P OUTPUT ACCEPT` reversed the default drop and
  granted the sandbox unrestricted egress.
- **Checkpoint HMAC widened** (`internal/replay`) — The signature covered only
  session ID, snapshot ID, checkpoint number and timestamp. The recorded
  command, arguments, exit code, duration, captured output, archive path, size
  and environment snapshot were unauthenticated and could be rewritten while
  the signature still verified. Now covers every immutable field, NUL-separated,
  and fails closed when no signing key is set.
- **Cost checksum widened** (`internal/cost`) — Covered only the derived totals.
  The raw usage counters, `network_cost` and `rate_id` were unprotected, so the
  evidence behind a charge could be rewritten. Float formatting changed from
  `%f` (which truncated at six decimals and collided for sub-microdollar
  amounts) to `strconv.FormatFloat('g', -1, 64)`. Added `Tracker.VerifyMetric`.
- **SSRF filter completed** (`internal/browser`) — `isPrivateIP` covered only
  `10/8`, `172.16/12` and `192.168/16`. Added loopback, link-local
  (`169.254/16`, which contains the cloud metadata endpoints), CGNAT
  (`100.64/10`), `0.0.0.0/8`, reserved ranges and the IPv6 private ranges.
  Added the AWS ECS metadata endpoint. Metadata hostnames are now blocked by
  name. The URL scheme is checked before DNS resolution, and every resolved
  address must pass rather than just the first.
- **Python literal escaping completed** (`internal/browser`) — `escapeString`
  escaped only single quotes, so a backslash or newline in a selector or form
  value could terminate the generated literal and append arbitrary Python.
- **`cost_metrics` deletion blocked** (migration 015) — The immutability trigger
  was `BEFORE UPDATE` only, so a billing row could be erased. Extended to
  `DELETE` while still permitting the cascade from `sessions`. The same
  protection added to `replay_checkpoints`.
- **WebSocket origin checked** (`cmd/api/handlers/collab.go`) — `CheckOrigin`
  returned `true` unconditionally, so any page a user visited could open a
  collaboration socket with their credentials. Now checks
  `CORS_ALLOWED_ORIGINS`; a missing `Origin` header is still allowed for
  non-browser clients.
- **`replay_enabled` enforced** (`internal/replay`) — The API handler did not
  check the flag, so a workspace and environment snapshot could be recorded for
  a session that never opted in. The check moved into the manager, covering both
  the handler and the runner hook.

### Fixed — Correctness

- **Checkpoint creation always failed** (`internal/replay`) — The number
  allocation ran `SELECT MAX(…) … FOR UPDATE`, which PostgreSQL rejects for
  aggregate queries, so every `POST /sessions/:id/checkpoints` returned 500.
  Replaced with a transaction-scoped advisory lock held across allocation,
  signing and insert. Numbering now starts at 1.
- **Cost summaries always failed** (`internal/cost`) — `SessionCostSummary` and
  `OrgCostSummary` had no `db:` tags, so sqlx failed with *missing destination
  name session_id*. `FirstMetric`/`LastMetric` are now pointers, since
  `MIN`/`MAX` return NULL for a session with no metrics.
- **Session listing broken** (`internal/models`) — `models.Session` was missing
  all seven columns added by migrations 011–014, so any query using
  `SELECT s.*` failed and `GET /api/v1/sessions` returned 500 for every
  non-master caller.
- **Migration 012 could not apply** — Referenced a non-existent table `orgs`
  instead of `organizations`, so `cost_budgets` and `cost_alerts` were never
  created on a fresh database.
- **Session Replay API was never wired up** — `cmd/api/handlers/replay.go` and
  its six routes existed in a branch but had not been merged, so the feature had
  no HTTP surface despite the dashboard UI shipping in v0.3.0.

### Added — Validation

- **Template validation** (`internal/templates/validate.go`) — Previously
  absent. Image references reject URL schemes, path traversal and shell
  metacharacters; resource limits reject zero, negative and absurd values;
  allowed hosts reject `*`, URLs and paths; env var names must be POSIX
  identifiers; slugs must be lowercase kebab-case; list sizes and the startup
  script are bounded. Invalid input now returns 400 rather than 500.
- **Collaboration validation** (`internal/collab/validate.go`) — Previously
  absent. Agent IDs reject `:` and glob characters, which would collide with
  sibling Redis keys in the `collab:session:<uuid>:` namespace. Message bodies
  bounded at 64 KB and required to be valid UTF-8. Message types and agent
  statuses restricted to known values. `max_agents` capped at 32.
- **Checkpoint metadata bounds** (`internal/replay`) — Name, description,
  command, argument count and environment variable count are bounded.
- **WebSocket rejection ordering** — Validation ran after `upgrader.Upgrade`,
  where an HTTP error can no longer be written. All checks now precede the
  upgrade, and a failed upgrade releases the agent slot.

### Added — Tests

- `internal/browser/security_test.go` — 28 SSRF payloads, 28 IP boundary
  values, Python literal escaping verified by scanning output rather than
  matching payloads.
- `internal/nlpolicy/security_test.go` — Rego and iptables injection, with
  positive cases confirming normal policies still compile.
- `internal/replay/security_test.go` — 16 checkpoint tamper cases.
- `internal/cost/security_test.go` — usage-counter tamper cases, float
  precision, timezone normalisation.
- `internal/templates/validate_test.go`, `internal/collab/validate_test.go` —
  ~110 validation cases.
- `cmd/api/handlers/features_e2e_test.go` — end-to-end lifecycle, cross-session
  and cross-tenant isolation, tamper detection at both the trigger and HMAC
  layers, malformed input, oversized bodies, audit coverage.

### Changed

- Audit action names for the new features now use constants in
  `internal/models` and follow the existing `<noun>.<past participle>`
  convention: `checkpoint.create` became `checkpoint.created`, and similarly for
  restore, fork and delete.
- Environment variable redaction in checkpoints replaced a list of specific
  names with generic substrings. `AWS_ACCESS_KEY_ID` was previously not
  redacted, along with SSH keys, session and cookie secrets, salts, passphrases,
  certificates and connection strings.

---

## [0.3.0] — 2026-07-30

**Major Feature Release** — 6 killer features for AI agent platforms.

### Added — Session Replay & Time-Travel Debugging

- **Checkpoint System** (`internal/replay`) — Capture full session state (files + metadata) at any moment
- **HMAC Signing** — Tamper detection for checkpoints via HMAC-SHA256
- **Workspace Snapshots** — Gzip-compressed tar archives stored in file vault
- **Sensitive Data Redaction** — Environment variables masked from checkpoints
- **Resource Limits** — Max 100 checkpoints/session, 500MB/checkpoint, 50GB/org
- **API Endpoints** — `POST /sessions/:id/checkpoints`, `GET /checkpoints/:id`, `POST /checkpoints/:id/restore`, `POST /checkpoints/:id/fork`
- **Migration 011** — `replay_checkpoints` table with JSONB state + archive paths
- **Dashboard UI** — Checkpoints tab in session detail page

### Added — Browser Automation Layer

- **Playwright Integration** (`internal/browser`) — Headless Chromium via Python + Playwright
- **SSRF Protection** — Blocks private IPs, localhost, cloud metadata endpoints (AWS/GCP/Azure)
- **8 MCP Tools** — `browser_navigate`, `browser_screenshot`, `browser_click`, `browser_fill`, `browser_extract`, `browser_evaluate`, `browser_wait`, `browser_pdf`
- **Artifact Capture** — Screenshots + PDFs saved as artifacts with checksums
- **Network Policy** — Respects session `allowed_hosts` for security
- **Docker Image** — `docker/browser-playwright-python.Dockerfile` with Chromium pre-installed

### Added — Cost Intelligence Dashboard

- **Cost Tracking** (`internal/cost`) — Real-time resource usage tracking per session
- **HMAC Signed Metrics** — Immutable cost records with tamper detection
- **Budget Alerts** — Org-level spending limits with configurable alerts
- **Cost Breakdown** — Compute, storage, network costs with AWS pricing defaults
- **PostgreSQL Trigger** — Prevents UPDATE/DELETE on `cost_metrics` for immutability
- **API Endpoints** — `GET /sessions/:id/costs`, `GET /costs/breakdown`, `GET /costs/alerts`, `POST /orgs/:id/budget`
- **Migration 012** — `cost_rates`, `cost_metrics`, `cost_budgets`, `cost_alerts` tables
- **Dashboard UI** — `/costs` page with total cost, breakdown, top spenders, alerts

### Added — Natural Language Policy Engine

- **LLM-Powered Parser** (`internal/nlpolicy`) — Converts natural language → structured security policies
- **OpenAI Integration** — Uses GPT models to generate policies from plain English
- **Policy Compiler** — Generates OPA Rego, Docker configs, iptables rules from structured policies
- **4 Built-in Templates** — `python-data-science`, `nodejs-web-app`, `security-audit`, `unrestricted-dev`
- **Mock Parser** — For testing without OpenAI API key
- **API Endpoints** — `POST /policies/parse`, `POST /policies/compile`, `GET /policies/templates`, `POST /policies/from-template/:name`
- **20+ Example Policies** — `examples/nlpolicy/example-policies.md`

### Added — Session Templates Marketplace

- **Template System** (`internal/templates`) — Pre-configured session templates with Docker image, resources, network, env vars
- **4 Built-in Templates** — Python Data Science, Node.js API, Web Scraping, Rust Development
- **CRUD Operations** — Create, list, get, update, delete custom templates
- **Usage Tracking** — Auto-increment `use_count` via PostgreSQL trigger
- **Template-Based Sessions** — `POST /templates/:id/use` creates session with template config
- **Full-Text Search** — GIN indexes on template name + description
- **API Endpoints** — `GET /templates`, `POST /templates`, `POST /templates/:id/use`
- **Migration 013** — `session_templates`, `template_usage` tables

### Added — Multi-Agent Collaboration

- **WebSocket Server** (`internal/collab`) — Real-time agent presence + messaging
- **Redis Pub/Sub** — Cross-instance event broadcasting for multi-server deployments
- **Agent Presence** — Track active agents, current file, last activity
- **Agent Messaging** — Direct messages + broadcast messages between agents
- **File Versioning** — Incremental version tracking for conflict detection
- **Hub Pattern** — Manages WebSocket connections + pub/sub subscriptions
- **Max Agents Limit** — Configurable per session (default 4)
- **API Endpoints** — `GET /sessions/:id/ws`, `GET /sessions/:id/agents`, `POST /sessions/:id/messages`
- **Migration 014** — `session_agents`, `agent_messages`, `file_versions` tables
- **WebSocket Protocol** — `agent_joined`, `agent_left`, `message`, `file_changed`, `presence_update`, `ping`/`pong`

### Added — Security & Testing

- **Security Testing Report** (`docs/security-testing-report.md`) — Comprehensive penetration testing + edge case analysis
- **6 Attack Scenarios** — All blocked (session hijacking, SSRF, cost manipulation, template injection, message flooding, checkpoint tampering)
- **OWASP Top 10** — All mitigated (SQL injection, XSS, SSRF, auth bypass, etc.)
- **Rate Limiting** — Global + per-actor limits, Redis-backed for distributed deployments
- **HMAC Signatures** — Applied to checkpoints, cost metrics, audit logs for tamper detection
- **Container Isolation** — Docker exec API, no shell injection, resource limits enforced

### Changed

- **MCP Tools Count** — 53 → 61 tools (+8 browser automation)
- **README** — Added "Killer Features" section highlighting 6 new features
- **Documentation** — All features have dedicated docs in `docs/features/`
- **Dependencies** — Added `github.com/gorilla/websocket`, upgraded `github.com/redis/go-redis/v9`

### Security

- ✅ All 6 features tested for security (see `docs/security-testing-report.md`)
- ✅ Input validation on all endpoints
- ✅ RBAC enforced for multi-tenant isolation
- ✅ Audit logging for all privileged operations
- ✅ Production-ready security posture

---

## [0.2.1] — 2026-07-15

Packaging, website, and Enterprise acquisition clarity — no API breaking changes.

### Added

- **Enterprise acquisition paths** on [vaultrun.dev/#enterprise](https://vaultrun.dev/#enterprise) — Evaluate / License / Talk to us, intake form → `mail@030.dev`
- **Enterprise one-pager** at [vaultrun.dev/enterprise.html](https://vaultrun.dev/enterprise.html) for procurement
- **Discoverability** — `llms.txt`, `robots.txt`, `sitemap.xml`, JSON-LD, `CITATION.cff`
- **PyPI** — `vaultrun-sdk` 0.2.1 with richer keywords and description

### Changed

- README / SSO guide / roadmap updated for open-core + Enterprise licensing paths
- Frontend landing and sidebar version badges set to v0.2.0+

### Fixed

- Duplicate Next.js `/dashboard` routes breaking CI frontend build
- GitHub Pages workflow `enablement` permission failure
- PyPI Trusted Publishing (OIDC) publisher / environment mismatch

---

## [0.2.0] — 2026-06-11

Open-core split. This repository is now the Apache 2.0 core; enterprise
features (SSO: OIDC + SAML 2.0) moved to the separate, privately licensed
VaultRun Enterprise repository and compile into the API server as an overlay.

### Changed

- **SSO extracted to VaultRun Enterprise** — `internal/sso` and the `/auth/*` HTTP handlers now live in the enterprise repository and are compiled in with `go build -tags enterprise`
- **Core builds fail fast on SSO config** — a core binary refuses to start when `OIDC_*`/`SAML_*` env vars are set, instead of silently dropping authentication routes
- `middleware.APIKeyAuth` now accepts a `SessionVerifier` interface instead of the concrete SSO session manager, decoupling core middleware from enterprise code
- README, landing page, and SSO setup guide updated for the edition split

### Fixed

- Stale `APIKeyAuth`/`newRouter` call signatures in the (build-tagged) integration and e2e test files, which no longer compiled

---

## [0.1.1] — 2026-06-11

Distribution release — functionally identical to 0.1.0, adds licensing, packaging,
and the project website.

### Added

- **LICENSE** — Apache 2.0 license text added (previously only referenced in the README)
- **Landing page** (`site/`) — [vaultrun.dev](https://vaultrun.dev), deployed via GitHub Pages (`.github/workflows/pages.yml`) with a contact form
- **PyPI publishing** (`.github/workflows/pypi.yml`) — `vaultrun-sdk` built, tested, and published via PyPI Trusted Publishing on release tags
- **Python SDK packaging** — `sdk/python/README.md`, classifiers, keywords, and project URLs for the PyPI listing
- `SECURITY.md` and `CONTRIBUTING.md`

### Fixed

- README: Go prerequisite corrected to 1.25+ (matches `go.mod`)

---

## [0.1.0] — 2026-06-11

First tagged release. Establishes the core platform, the full MCP server feature set,
and enterprise SSO.

### Added — SSO / Identity Federation

- **OIDC** (`internal/sso/oidc.go`) — Authorization Code + PKCE flow with IdP discovery; supports Okta, Azure AD, Google Workspace, Keycloak, Auth0
- **SAML 2.0** (`internal/sso/saml.go`) — Service Provider implementation via `crewjam/saml`; HTTP-POST ACS binding, email attribute mapping, `goxmldsig` XMLDSig validation
- **JWT session cookies** (`internal/sso/session.go`) — HS256 cookies via `lestrrat-go/jwx/v3`; `HttpOnly`, `Secure`, configurable lifetime
- **SSO routes** — `GET /auth/oidc/login`, `GET /auth/oidc/callback`, `GET /auth/saml/metadata`, `GET /auth/saml/login`, `POST /auth/saml/acs`, `GET /auth/me`, `POST /auth/logout`
- **Migration 010** — `sso_users` table mapping external identity (OIDC `sub` / SAML `NameID`) to VaultRun API key; key auto-provisioned on first login
- **Auth middleware** updated to accept session cookies alongside existing `X-API-Key` / `Bearer` header authentication
- **Fail-safe startup** — server exits on startup if `SSO_SESSION_SECRET` is empty when SSO is configured

### Added — Multi-Region

- `REGION` env var — included in `/health` response for operational visibility
- `DATABASE_READ_URL` — optional read-replica DSN; routes list/get queries to replica, writes go to primary
- `docs/multi-region.md` — deployment guide covering active-passive, active-active (CockroachDB/Citus), session affinity, Redis failover, and Prometheus multi-region scrape config

### Added — SDK additions

- **Go SDK** (`sdk/go`): `Image`, `SessionStats`, `PullStatus` types; `GetSessionStats()`, `GetSessionLogs()`, `ListImages()`, `PullImage()` methods
- **Python SDK** (`sdk/python`): same four methods + dataclasses; 4 new test cases (31 total)

### Added — Dashboard security

- **Server-side API proxy** (`apps/frontend/src/app/api/proxy/[...path]/route.ts`) — all dashboard API calls routed through a Next.js server-side proxy; `VAULTRUN_API_KEY` is never exposed in the browser bundle
- Docker Compose: `VAULTRUN_API_URL` and `VAULTRUN_API_KEY` added to frontend service

### Security fixes (SSO hardening — applied after initial implementation)

- **C-1** — SAML InResponseTo validation: `LoginURL` now returns the `AuthnRequest` ID; it is stored in a `SameSite=Strict` HttpOnly cookie and passed to `ParseResponse`, preventing SAML response replay attacks
- **C-2** — OIDC ID token signature verified against IdP JWKS (`lestrrat-go/jwx/v3`); `iss`, `aud`, `exp`, and `nonce` claims validated — forged tokens are rejected regardless of TLS state
- **H-2** — IdP `error` query parameter no longer reflected in OIDC callback response (attacker-controlled); logged server-side via `slog.Warn` including `error_description`
- **H-3** — Server-side session invalidation: every JWT carries a unique `jti`; `logout` adds it to Redis (TTL = remaining session lifetime) so stolen tokens are immediately rejected — requires `REDIS_ADDR`; graceful no-op fallback when Redis is absent
- **H-4** — `SSO_SESSION_SECRET` minimum length enforced at startup: server exits if secret is shorter than 32 bytes
- **H-5** — `SameSite=Lax` on session cookie; `SameSite=Strict` on all pre-auth cookies (`oidc_state`, `oidc_verifier`, `oidc_nonce`, `saml_request_id`); deletion uses matching flags to ensure browser compliance
- **H-6** — OIDC `nonce` generated, stored in cookie, sent in authorization URL, and verified in JWKS-validated ID token — prevents ID token replay at the token endpoint
- **M-1** — Removed dead `authpkg.Validate("","")` call in SSO middleware branch that issued a spurious DB query per SSO-authenticated request
- **M-2** — `upsertSSOUser` wrapped in `BEGIN … SELECT FOR UPDATE … COMMIT` transaction; eliminates TOCTOU race on concurrent first-logins
- **M-3** — `Secure` cookie flag derived from `sessionMgr.Secure()` (TLS state) rather than whether the session object is non-nil; deletion uses the same flag as creation
- **M-5** — `SAML_IDP_METADATA_FILE` loads IdP metadata from a local file, eliminating MITM risk on the live metadata URL; `SAML_IDP_METADATA_URL` remains the fallback
- **M-6** — `email` included in the existing-user `UPDATE` so IdP email changes are reflected in audit log actor entries
- **L-1** — `RateLimit(30)` applied to OIDC login/callback, SAML login, and SAML ACS endpoints
- **L-2** — `GenerateState` increased from 16 to 32 bytes (256 bits, per RFC 9126)
- **L-3** — IdP `error_description` parameter logged server-side for diagnostics (not returned to client)
- **L-4** — `GET /auth/me` uses API key UUID already set in Gin context by `APIKeyAuth` middleware instead of re-parsing the session JWT
- **L-6** — `POST /auth/saml/acs` validates `Content-Type: application/x-www-form-urlencoded` and returns `415` for other content types
- **I-2** — OIDC JWKS key set cached for 15 minutes with double-checked locking; stale cache returned on transient fetch errors to avoid blocking logins during IdP downtime

### Changed

- `docs/configuration.md` — SSO, multi-region, and MCP server sections added
- `docs/security.md` — SSO security model, updated controls table, and production checklist extended to 21 items
- `docs/roadmap.md` — v0.7 (MCP/CI/DB/AWS) and v0.8 (dashboard) marked complete
- `.env.example` — SSO, multi-region, MCP server, CI runner, frontend proxy, and SAML metadata file sections

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

[0.2.1]: https://github.com/nickvd7/vaultrun/releases/tag/v0.2.1
[0.2.0]: https://github.com/nickvd7/vaultrun/releases/tag/v0.2.0
[0.1.1]: https://github.com/nickvd7/vaultrun/releases/tag/v0.1.1
[0.1.0]: https://github.com/nickvd7/vaultrun/releases/tag/v0.1.0
