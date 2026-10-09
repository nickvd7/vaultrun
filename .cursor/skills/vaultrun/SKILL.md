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

> Marketplace copy: keep in sync with [skills/vaultrun/SKILL.md](../../skills/vaultrun/SKILL.md). Publish guide: [docs/plugin-publish.md](../../docs/plugin-publish.md).

## Product

Self-hosted secure runtime for AI agents. Agents execute code, query databases, and call APIs inside isolated Docker containers on the operator's infrastructure. No SaaS, no telemetry.

- **Core:** Apache 2.0 — API, CLI, MCP, CI runner, dashboard, Go/Python SDKs
- **Enterprise:** OIDC + SAML SSO (commercial overlay) — `docs/sso-setup.md`
- **Workflows:** missions, verify checkpoints + controls + sealed evidence, optional OpenJEV/TypeSafe Jev claim gates

## Repo map

| Path | Role |
|------|------|
| `cmd/api/` | Gin REST API — sessions, runs, files, audit, keys, verify, jev |
| `cmd/cli/` | `vaultrun` CLI |
| `cmd/ci-runner/` | GitHub webhook → sandbox CI |
| `cmd/local/` | OpenAI-compat local inference action gateway |
| `internal/` | auth, docker, workspace, audit, policy, db, localgateway, verify, jev |
| `sdk/mcp/` | MCP server — build with `go build -o vaultrun-mcp ./sdk/mcp/` |
| `sdk/go/`, `sdk/python/` | Client SDKs |
| `apps/frontend/` | Next.js dashboard (`Sidebar`, `AppShell`, `api.ts`) |
| `site/` | Static marketing HTML + `nav.css` / `nav.js` + `llms.txt` |
| `docs/` | architecture, security, mcp, openapi.yaml, features/* |
| `deployments/` | Docker Compose |

## Conventions

- Go 1.25; minimal focused diffs
- API auth: `X-API-Key` header (`vr_...`)
- MCP optional features use explicit env opt-in (`MCP_AWS_ENABLED`, `MCP_FLOWD_ENABLED`, `MCP_JEV_ENABLED`)
- Marketing site: monospace dark theme; shared nav in `site/nav.css`
- Do not commit secrets or edit the plan file in `.cursor/plans/`

## Common tasks

### Local dev

```bash
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

- Checkpoints: `POST /api/v1/verify`, MCP `verify_checkpoint`
- Controls v2: `GET|POST /api/v1/verify/controls`, MCP `verify_controls`
- Evidence: `POST /api/v1/verify/evidence`, MCP `verify_evidence`
- Docs: `docs/features/verify-*.md`

### OpenJEV / Jev claim gates (opt-in)

`OPENJEV_API_KEY` / `TYPESAFE_API_KEY` + `MCP_JEV_ENABLED` / `VAULTRUN_JEV_ENABLED` / `LOCAL_GATEWAY_JEV_ENABLED`. MCP: `jev_verify`, `jev_gate`. See [docs/features/jev-integration.md](../../docs/features/jev-integration.md).

### Flowd bridge

Set `MCP_FLOWD_ENABLED=true` and install `flowctl`. See [docs/flowd-integration.md](../../docs/flowd-integration.md).

### Add MCP tool

1. Define in `sdk/mcp/tools.go` (`toolDefinitions` + `callTool` switch)
2. Implement handler (new file if large, e.g. `flowd.go`, `aws.go`, `jev.go`)
3. Document in `sdk/mcp/README.md` and `docs/mcp.md`

## API quick reference

Base: `/api/v1` — full schema in `docs/openapi.yaml`

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
- LLM product summary: [site/llms.txt](../../site/llms.txt)
- Security model: [docs/security.md](../../docs/security.md)
- Jev / OpenJEV: [docs/features/jev-integration.md](../../docs/features/jev-integration.md)
- Plugin publish: [docs/plugin-publish.md](../../docs/plugin-publish.md)
