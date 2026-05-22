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
- [ ] Organizations / teams
- [ ] Per-org API key namespacing
- [ ] RBAC (read-only vs. execute vs. admin)
- [ ] Session sharing within org

## v0.4 — Advanced Runners
- [ ] Kubernetes runner backend
- [ ] Firecracker microVM runner
- [ ] Runner pool with scheduling
- [ ] Warm container pool for low-latency startup

## v0.5 — Policy Engine
- [ ] Open Policy Agent (OPA) integration
- [ ] Per-session command allowlist/denylist
- [ ] File access policies (read-only mounts, path restrictions)
- [ ] Network egress policies

## v0.6 — Secrets & State
- [ ] Secrets broker (Vault / AWS Secrets Manager integration)
- [ ] Persistent workspace snapshots
- [ ] Session resume from snapshot
- [ ] Cross-session artifact sharing

## v1.0 — Enterprise
- [ ] SSO / SAML
- [ ] Enterprise audit export (SIEM integrations)
- [ ] Hosted control plane option
- [ ] GPU runner support
- [ ] Agent SDK integrations (LangChain, CrewAI, AutoGen)
- [ ] Multi-region support
