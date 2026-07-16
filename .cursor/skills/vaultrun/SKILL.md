---
name: vaultrun
description: >-
  Develops and operates VaultRun, a self-hosted secure runtime for AI agents with
  Docker sandboxes, MCP tools (stdio + HTTP), Go API, and Next.js dashboard. Use
  when working on vaultrun, agent sandboxes, MCP server, VaultRun API, dashboard,
  marketing site, Flowd integration, or Enterprise SSO documentation.
---

# VaultRun

> Marketplace copy: keep in sync with [skills/vaultrun/SKILL.md](../../skills/vaultrun/SKILL.md). Publish guide: [docs/plugin-publish.md](../../docs/plugin-publish.md).

## Product

Self-hosted secure runtime for AI agents. Agents execute code, query databases, and call APIs inside isolated Docker containers on the operator's infrastructure. No SaaS, no telemetry.

- **Core:** Apache 2.0 — API, CLI, MCP, CI runner, dashboard, Go/Python SDKs
- **Enterprise:** OIDC + SAML SSO (commercial overlay) — `docs/sso-setup.md`

## Repo map

| Path | Role |
|------|------|
| `cmd/api/` | Gin REST API — sessions, runs, files, audit, keys |
| `cmd/cli/` | `vaultrun` CLI |
| `cmd/ci-runner/` | GitHub webhook → sandbox CI |
| `internal/` | auth, docker, workspace, audit, policy, db |
| `sdk/mcp/` | MCP server — build with `go build -o vaultrun-mcp ./sdk/mcp/` |
| `sdk/go/`, `sdk/python/` | Client SDKs |
| `apps/frontend/` | Next.js dashboard (`Sidebar`, `AppShell`, `api.ts`) |
| `site/` | Static marketing HTML + `nav.css` / `nav.js` |
| `docs/` | architecture, security, mcp, openapi.yaml |
| `deployments/` | Docker Compose |

## Conventions

- Go 1.25; minimal focused diffs
- API auth: `X-API-Key` header (`vr_...`)
- MCP optional features use explicit env opt-in (`MCP_AWS_ENABLED`, `MCP_FLOWD_ENABLED`)
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

### Flowd bridge

Set `MCP_FLOWD_ENABLED=true` and install `flowctl`. See [docs/flowd-integration.md](../../docs/flowd-integration.md).

### Add MCP tool

1. Define in `sdk/mcp/tools.go` (`toolDefinitions` + `callTool` switch)
2. Implement handler (new file if large, e.g. `flowd.go`, `aws.go`)
3. Document in `sdk/mcp/README.md` and `docs/mcp.md`

## API quick reference

Base: `/api/v1` — full schema in `docs/openapi.yaml`

| Resource | Endpoints |
|----------|-----------|
| Sessions | `POST/GET/DELETE /sessions`, `GET /sessions/:id` |
| Runs | `POST /sessions/:id/runs`, SSE stream |
| Files | upload/download/list in session workspace |
| Keys | `POST/GET/DELETE /keys` (master key required) |
| Audit | `GET /audit` |

## Additional resources

- API/env details: [reference.md](reference.md)
- LLM product summary: [site/llms.txt](../../site/llms.txt)
- Security model: [docs/security.md](../../docs/security.md)
- Plugin publish: [docs/plugin-publish.md](../../docs/plugin-publish.md)
