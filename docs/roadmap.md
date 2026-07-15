# VaultRun Roadmap

Status as of **v0.2.1** (2026-07-15).

## Shipped

### MVP (v0.1)
- [x] Session management API
- [x] Docker sandbox runner (one container per session)
- [x] File vault (upload / download / list)
- [x] Command execution via Docker exec (no shell injection)
- [x] Audit logging
- [x] API key authentication
- [x] React dashboard
- [x] Go CLI
- [x] Go + Python SDKs
- [x] Docker Compose deployment
- [x] Unit + integration tests

### v0.2 — Hardening
- [x] Seccomp profiles for containers
- [x] Image allowlist in policy hook
- [x] Per-session network policies (allowlist specific hosts)
- [x] TLS termination (HTTPS via TLS_CERT_FILE/TLS_KEY_FILE; Let's Encrypt/ACME planned)
- [x] Rate limiting (IP + per-actor; Redis-backed sliding window planned)
- [x] Session idle timeout cleanup (background goroutine)
- [x] Streaming run output via SSE
- [x] Run artifacts: automatic detection of new files post-run

### v0.3 — Multi-tenancy
- [x] Organizations / teams (`POST/GET/DELETE /api/v1/orgs`, slug auto-generation)
- [x] Per-org API key namespacing (`org_id` FK on `api_keys`; keys scoped to org on creation)
- [x] RBAC (viewer / executor / admin roles on `org_members`; enforced per-endpoint)
- [x] Session sharing within org (`org_id` on sessions; org members see shared sessions in `GET /sessions`; `GET /orgs/:id/sessions`)

### v0.4 — Advanced runners (partial)
- [x] Warm container pool for low-latency startup (`WARM_POOL_SIZE` / `WARM_POOL_IMAGE`)
- [x] GPU runner support (`DOCKER_GPU_DEVICES`; `gpu_enabled` session flag)

### v0.5 — Policy engine
- [x] Open Policy Agent (OPA) integration
- [x] Per-session command allowlist/denylist
- [x] File access policies
- [x] Network egress policies (per-session iptables allowlist)

### v0.6 — Secrets & state
- [x] Secrets broker (Vault KV v2 + AWS Secrets Manager + env fallback)
- [x] Persistent workspace snapshots
- [x] Session resume from snapshot
- [x] Cross-session artifact sharing

### v0.7 — MCP & AI integrations
- [x] MCP server — stdio + HTTP, 53 tools
- [x] MCP security (Bearer auth, rate limits, TLS, audit redaction)
- [x] CI Runner (`cmd/ci-runner/`)
- [x] Database MCP tools (SQLite, PostgreSQL, MongoDB)
- [x] Go + Python SDK extensions
- [x] Agent SDK examples (LangChain + AutoGen)

### v0.8 — Dashboard
- [x] Next.js dashboard (`/dashboard`, sessions, audit, proxy)

### Enterprise (VaultRun Enterprise edition)
- [x] SSO — OIDC (PKCE) + SAML 2.0 (ships in private Enterprise overlay; `go build -tags enterprise`)
- [x] Enterprise audit export (`AUDIT_EXPORT_URL` / `AUDIT_EXPORT_SECRET`)
- [x] Acquisition paths — [vaultrun.dev/#enterprise](https://vaultrun.dev/#enterprise) · [one-pager](https://vaultrun.dev/enterprise.html)

Get Enterprise: evaluate (free for dev/test), production license, or sales — requests to **mail@030.dev**.

## Future / deferred

These remain intentional non-goals until there is clear demand and infrastructure capacity:

| Item | Why deferred |
|---|---|
| Kubernetes runner backend | Needs cluster-oriented scheduling & ops story |
| Firecracker microVM runner | Requires host kernel / nested virt support |
| Shared runner pool with scheduling | Depends on Kubernetes (or equivalent) control plane |
| Hosted control plane (SaaS) | Conflict with self-hosted positioning; separate product decision |
| Multi-region active-active | Complex consistency & networking; see docs/multi-region.md notes |

## Near-term (backlog hints)

- Let's Encrypt / ACME helpers for API TLS
- Redis-backed sliding-window rate limiting (beyond in-memory)
- Richer dashboard Session UX and org switcher
- Optional public “starting from” commercial packaging once pricing is finalized
