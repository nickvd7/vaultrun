---
name: vaultrun
description: >-
  Develops and operates VaultRun, a self-hosted secure runtime for AI agents with
  Docker sandboxes, MCP tools (stdio + HTTP), verify evidence, optional OpenJEV/Jev
  claim gates, Go API, and Next.js dashboard. Use when working on vaultrun, agent
  sandboxes, MCP server, VaultRun API, dashboard, marketing site, Flowd integration,
  verify/Jev gates, or Enterprise SSO documentation.
---

# VaultRun

## Product

Self-hosted secure runtime for AI agents. Agents execute code, query databases, and call APIs inside isolated Docker containers on the operator's infrastructure. No SaaS, no telemetry.

- **Core:** Apache 2.0 — API, CLI, MCP, CI runner, dashboard, Go/Python SDKs
- **Enterprise:** OIDC + SAML SSO (commercial overlay)
- **Workflows:** missions, verify checkpoints + controls + sealed evidence, optional OpenJEV/TypeSafe Jev claim gates

## Repo map

| Path | Role |
|------|------|
| `cmd/api/` | Gin REST API — sessions, runs, files, audit, keys, verify, jev |
| `cmd/cli/` | `vaultrun` CLI |
| `cmd/ci-runner/` | GitHub webhook → sandbox CI |
| `cmd/local/` | OpenAI-compat local inference action gateway |
| `internal/` | auth, docker, workspace, audit, policy, db, localgateway, verify, jev |
| `sdk/mcp/` | MCP server — `go build -o vaultrun-mcp ./sdk/mcp/` |
| `sdk/go/`, `sdk/python/` | Client SDKs |
| `apps/frontend/` | Next.js dashboard |
| `site/` | Static marketing HTML + `llms.txt` / `llms-full.txt` |
| `docs/` | architecture, security, mcp, openapi.yaml, features/* |
| `deployments/` | Docker Compose |

## Conventions

- Go 1.25; minimal focused diffs
- API auth: `X-API-Key` header (`vr_...`)
- MCP optional features use explicit env opt-in (`MCP_AWS_ENABLED`, `MCP_FLOWD_ENABLED`, `MCP_JEV_ENABLED`)
- Never commit secrets

## Common tasks

### Local dev

```bash
git clone https://github.com/nickvd7/vaultrun && cd vaultrun
cp .env.example .env
make up && make bootstrap-key
curl http://localhost:8080/health
```

### MCP (stdio)

```json
{
  "mcpServers": {
    "vaultrun": {
      "command": "/path/to/vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_..."
      }
    }
  }
}
```

### Verify + sealed evidence

- Checkpoints: `POST /api/v1/verify`, MCP `verify_checkpoint` (`exit_code_zero` / `stdout_contains` / `file_exists`)
- Controls v2: `GET|POST /api/v1/verify/controls`, MCP `verify_controls` (anti-shortcut suite)
- Evidence: `POST /api/v1/verify/evidence`, MCP `verify_evidence` (`content_digest` + optional HMAC)
- Docs: `docs/features/verify-checkpoints.md`, `verify-controls.md`, `verify-evidence.md`

### OpenJEV / Jev claim gates (opt-in)

Set provider key (`OPENJEV_API_KEY` and/or `TYPESAFE_API_KEY`) plus surface flag:

| Surface | Env |
|---------|-----|
| MCP | `MCP_JEV_ENABLED=true` → tools `jev_verify`, `jev_gate` |
| API | `VAULTRUN_JEV_ENABLED=true` → `/api/v1/verify/jev(-gate)` |
| Local gateway | `LOCAL_GATEWAY_JEV_ENABLED=true` → completion gate |

Mission steps may set `jev_claim`, `jev_min_noul`, `jev_on_fail` (`fail`|`hold`). See `docs/features/jev-integration.md` and https://openjev.sh/.

### Flowd bridge

Set `MCP_FLOWD_ENABLED=true` and install `flowctl`. See flowd-integration doc (link below).

### Add MCP tool

1. Define in `sdk/mcp/tools.go` (`toolDefinitions` + `callTool` switch)
2. Implement handler (e.g. `flowd.go`, `aws.go`, `jev.go`)
3. Document in `sdk/mcp/README.md` and `docs/mcp.md`

## API quick reference

Base: `/api/v1` — full schema in OpenAPI (link below)

| Resource | Endpoints |
|----------|-----------|
| Sessions | `POST/GET/DELETE /sessions`, `GET /sessions/:id` |
| Runs | `POST /sessions/:id/runs`, SSE stream |
| Files | upload/download/list in session workspace |
| Verify | `POST /verify`, `/verify/controls`, `/verify/evidence`, `/verify/jev(-gate)` |
| Keys | `POST/GET/DELETE /keys` (master key required) |
| Audit | `GET /audit` |

## Additional resources

- API/env details: [reference.md](reference.md)
- Product site: https://vaultrun.dev
- LLM grounding: https://vaultrun.dev/llms.txt · https://vaultrun.dev/llms-full.txt
- Security: https://github.com/nickvd7/vaultrun/blob/main/docs/security.md
- Jev / OpenJEV: https://github.com/nickvd7/vaultrun/blob/main/docs/features/jev-integration.md
- Flowd integration: https://github.com/nickvd7/vaultrun/blob/main/docs/flowd-integration.md
- OpenAPI: https://github.com/nickvd7/vaultrun/blob/main/docs/openapi.yaml
- Enterprise SSO: https://github.com/nickvd7/vaultrun/blob/main/docs/sso-setup.md
