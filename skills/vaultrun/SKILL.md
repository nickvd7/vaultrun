---
name: vaultrun
description: >-
  Develops and operates VaultRun, a self-hosted secure runtime for AI agents with
  Docker sandboxes, MCP tools (stdio + HTTP), Go API, and Next.js dashboard. Use
  when working on vaultrun, agent sandboxes, MCP server, VaultRun API, dashboard,
  marketing site, Flowd integration, or Enterprise SSO documentation.
---

# VaultRun

## Product

Self-hosted secure runtime for AI agents. Agents execute code, query databases, and call APIs inside isolated Docker containers on the operator's infrastructure. No SaaS, no telemetry.

- **Core:** Apache 2.0 — API, CLI, MCP, CI runner, dashboard, Go/Python SDKs
- **Enterprise:** OIDC + SAML SSO (commercial overlay)

## Repo map

| Path | Role |
|------|------|
| `cmd/api/` | Gin REST API — sessions, runs, files, audit, keys |
| `cmd/cli/` | `vaultrun` CLI |
| `cmd/ci-runner/` | GitHub webhook → sandbox CI |
| `internal/` | auth, docker, workspace, audit, policy, db |
| `sdk/mcp/` | MCP server — `go build -o vaultrun-mcp ./sdk/mcp/` |
| `sdk/go/`, `sdk/python/` | Client SDKs |
| `apps/frontend/` | Next.js dashboard |
| `site/` | Static marketing HTML |
| `docs/` | architecture, security, mcp, openapi.yaml |
| `deployments/` | Docker Compose |

## Conventions

- Go 1.25; minimal focused diffs
- API auth: `X-API-Key` header (`vr_...`)
- MCP optional features use explicit env opt-in (`MCP_AWS_ENABLED`, `MCP_FLOWD_ENABLED`)
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

### Flowd bridge

Set `MCP_FLOWD_ENABLED=true` and install `flowctl`. See flowd-integration doc (link below).

### Add MCP tool

1. Define in `sdk/mcp/tools.go` (`toolDefinitions` + `callTool` switch)
2. Implement handler (e.g. `flowd.go`, `aws.go`)
3. Document in `sdk/mcp/README.md` and `docs/mcp.md`

## API quick reference

Base: `/api/v1` — full schema in OpenAPI (link below)

| Resource | Endpoints |
|----------|-----------|
| Sessions | `POST/GET/DELETE /sessions`, `GET /sessions/:id` |
| Runs | `POST /sessions/:id/runs`, SSE stream |
| Files | upload/download/list in session workspace |
| Keys | `POST/GET/DELETE /keys` (master key required) |
| Audit | `GET /audit` |

## Additional resources

- API/env details: [reference.md](reference.md)
- Product site: https://vaultrun.dev
- LLM grounding: https://vaultrun.dev/llms.txt
- Security: https://github.com/nickvd7/vaultrun/blob/main/docs/security.md
- Flowd integration: https://github.com/nickvd7/vaultrun/blob/main/docs/flowd-integration.md
- OpenAPI: https://github.com/nickvd7/vaultrun/blob/main/docs/openapi.yaml
- Enterprise SSO: https://github.com/nickvd7/vaultrun/blob/main/docs/sso-setup.md
