# VaultRun Roadmap

## MVP (v0.1) — Current
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

## v0.2 — Hardening
- [x] Seccomp profiles for containers
- [x] Image allowlist in policy hook
- [x] Per-session network policies (allowlist specific hosts)
- [x] TLS termination (HTTPS via TLS_CERT_FILE/TLS_KEY_FILE; Let's Encrypt/ACME planned)
- [x] Rate limiting (IP + per-actor; Redis-backed sliding window planned)
- [x] Session idle timeout cleanup (background goroutine)
- [x] Streaming run output via SSE
- [x] Run artifacts: automatic detection of new files post-run

## v0.3 — Multi-tenancy
- [x] Organizations / teams (`POST/GET/DELETE /api/v1/orgs`, slug auto-generation)
- [x] Per-org API key namespacing (`org_id` FK on `api_keys`; keys scoped to org on creation)
- [x] RBAC (viewer / executor / admin roles on `org_members`; enforced per-endpoint)
- [x] Session sharing within org (`org_id` on sessions; org members see shared sessions in `GET /sessions`; `GET /orgs/:id/sessions`)

## v0.4 — Advanced Runners
- [ ] Kubernetes runner backend *(deferred — infrastructure-dependent)*
- [ ] Firecracker microVM runner *(deferred — requires host kernel support)*
- [ ] Runner pool with scheduling *(deferred — requires Kubernetes)*
- [x] Warm container pool for low-latency startup (`WARM_POOL_SIZE` / `WARM_POOL_IMAGE`; pre-started containers, non-blocking acquire on hot path)
- [x] GPU runner support (`DOCKER_GPU_DEVICES`; NVIDIA Container Toolkit passthrough via `gpu_enabled` session flag)

## v0.5 — Policy Engine
- [x] Open Policy Agent (OPA) integration (`OPA_POLICY_FILE`, Rego evaluation via `go-opa-evaluate`)
- [x] Per-session command allowlist/denylist (via OPA `EvalCommand` hook — deny by command + args)
- [x] File access policies (via OPA `EvalFileAccess` hook — deny by path pattern)
- [x] Network egress policies (per-session iptables allowlist via `AllowedHosts`; DNS + ESTABLISHED always permitted)

## v0.6 — Secrets & State
- [x] Secrets broker (Vault KV v2 + AWS Secrets Manager + env fallback; `SECRETS_PROVIDER`; injected into runs via `secrets` field)
- [x] Persistent workspace snapshots (`POST /sessions/:id/snapshots`, `GET /snapshots/:id/download`, `DELETE /snapshots/:id`)
- [x] Session resume from snapshot (`snapshot_id` in `POST /sessions` restores workspace before container start)
- [x] Cross-session artifact sharing (`POST /sessions/:id/artifacts`, `GET /artifacts`, `GET /artifacts/:id/download`, `DELETE /artifacts/:id`)

## v1.0 — Enterprise
- [ ] SSO / SAML *(deferred)*
- [x] Enterprise audit export (SIEM integrations — `AUDIT_EXPORT_URL`; newline-delimited JSON POST every 30 s; Bearer token auth via `AUDIT_EXPORT_SECRET`)
- [ ] Hosted control plane option *(deferred)*
- [x] GPU runner support (see v0.4 above)
- [x] Agent SDK integrations (LangChain + AutoGen examples in `sdk/python/examples/`)
- [ ] Multi-region support *(deferred)*
