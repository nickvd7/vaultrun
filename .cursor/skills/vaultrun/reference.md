# VaultRun reference

## Required MCP env

| Variable | Description |
|----------|-------------|
| `VAULTRUN_BASE_URL` | API base URL |
| `VAULTRUN_API_KEY` | `vr_...` API key |

## Optional MCP env

| Variable | Description |
|----------|-------------|
| `MCP_TRANSPORT` | `stdio` (default) or `http` |
| `MCP_AUTH_TOKEN` | Bearer token for HTTP transport |
| `MCP_AWS_ENABLED` | `true` → 14 AWS tools |
| `MCP_FLOWD_ENABLED` | `true` → 6 Flowd tools |
| `FLOWCTL_PATH` | Path to flowctl (default: `flowctl`) |
| `FLOWD_CONFIG` | flowctl `--config` path |
| `MCP_FS_ALLOWED_PATHS` | Comma-separated absolute paths for fs_* tools |
| `MCP_SQLITE_PATH` | SQLite file for sqlite_* tools |
| `MCP_PG_DSN` | Postgres DSN for pg_* tools |
| `MCP_MONGO_URI` / `MCP_MONGO_DB` | MongoDB tools |
| `GITHUB_TOKEN` | GitHub clone/comment tools |

## Tool counts

- Core: 53 tools (always)
- + AWS: 14 (when `MCP_AWS_ENABLED=true`)
- + DB: varies by configured databases
- + Flowd: 6 (when `MCP_FLOWD_ENABLED=true`)

## Makefile targets

| Target | Action |
|--------|--------|
| `make up` | Start Docker Compose stack |
| `make down` | Stop stack |
| `make bootstrap-key` | Create first API key |
| `make test` | Run tests |

## Frontend

- App routes under `apps/frontend/src/app/(app)/`
- `AppShell` + `Sidebar` — mobile drawer at `<lg` (1024px)
- API client: `apps/frontend/src/lib/api.ts`
