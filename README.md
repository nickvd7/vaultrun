# VaultRun

**Self-hosted secure runtime for AI agents.**

VaultRun lets AI agents safely execute code, query databases, call cloud APIs, and manage files inside isolated Docker sandboxes running on your own infrastructure. No external SaaS. No data leaving your network.

```
┌──────────────────────────────────────────────────────────────┐
│  Your AI Agent  (Claude, GPT-4o, custom, …)                 │
│                                                              │
│  result = client.run(session_id,                             │
│      command="python", args=["analyze.py"])                  │
└────────────────────────┬─────────────────────────────────────┘
                         │ API key
                         ▼
┌──────────────────────────────────────────────────────────────┐
│  VaultRun API  (your server, your infra)                     │
│                                                              │
│  • Isolated Docker container per session                     │
│  • exec API only — no shell injection                        │
│  • Path traversal prevention in workspace                    │
│  • HMAC-signed audit trail                                   │
│  • CPU / memory / timeout limits per session                 │
│  • Network disabled by default                               │
└──────────────────────────────────────────────────────────────┘
```

## Quickstart

**Prerequisites:** Docker, Docker Compose, Go 1.23+

```bash
git clone https://github.com/nickvd7/vaultrun
cd vaultrun
cp .env.example .env        # set MASTER_API_KEY to something strong
make up                     # start API + Postgres + Redis + dashboard
make bootstrap-key          # prints your first vr_... API key
curl http://localhost:8080/health
```

Open `http://localhost:3000` for the dashboard.

## What's included

| Component | Description |
|---|---|
| **API server** (`cmd/api`) | Gin-based REST API — sessions, runs, files, audit |
| **CLI** (`cmd/cli`) | `vaultrun` command-line tool |
| **MCP server** (`sdk/mcp`) | 53-tool MCP server (stdio + HTTP) |
| **CI runner** (`cmd/ci-runner`) | GitHub webhook → sandbox CI + Slack/Teams notify |
| **Dashboard** (`apps/frontend`) | Next.js management UI |
| **Go SDK** (`sdk/go`) | Typed Go client |
| **Python SDK** (`sdk/python`) | Python client |

## Architecture

```
vaultrun/
├── cmd/
│   ├── api/          Go API server (Gin)
│   ├── cli/          Go CLI (vaultrun)
│   └── ci-runner/    GitHub webhook CI runner
├── internal/
│   ├── auth/         API key hashing + validation
│   ├── audit/        HMAC-signed event logger
│   ├── docker/       Container + exec management
│   ├── workspace/    File vault, path traversal prevention
│   ├── runner/       Command execution orchestration
│   ├── db/           Postgres queries (sqlx)
│   ├── policy/       Pluggable policy hook (OPA-ready)
│   └── config/       Environment-based configuration
├── apps/
│   └── frontend/     Next.js dashboard (React + Tailwind)
├── sdk/
│   ├── go/           Go SDK
│   ├── python/       Python SDK
│   └── mcp/          MCP server (53 tools)
├── migrations/       SQL migrations (golang-migrate)
├── deployments/      Docker Compose + Dockerfiles
├── docs/             Architecture, security, API reference
└── examples/         Usage examples
```

## MCP Server (53 tools)

The MCP server exposes every VaultRun capability as a Model Context Protocol tool.
Add it to Claude Desktop, Claude Code, or any MCP-compatible platform in seconds.

### Stdio transport (Claude Desktop / Claude Code)

```json
{
  "mcpServers": {
    "vaultrun": {
      "command": "/path/to/vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_your_key"
      }
    }
  }
}
```

Build the binary: `go build -o vaultrun-mcp ./sdk/mcp/`

### HTTP transport (OpenAI / OpenRouter / custom)

```bash
MCP_TRANSPORT=http \
MCP_AUTH_TOKEN=your-secret-token \
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_your_key \
./vaultrun-mcp
# POST /mcp — JSON-RPC 2.0, Authorization: Bearer your-secret-token
```

### Tool categories

| Category | Tools |
|---|---|
| **Sandbox** | `create_session`, `list_sessions`, `get_session`, `delete_session`, `run_command`, `upload_file`, `read_file`, `list_files`, `delete_file`, `get_run`, `list_runs`, `get_session_stats`, `get_session_logs` |
| **Images** | `list_images`, `pull_image` |
| **Snapshots** | `create_snapshot`, `list_snapshots` |
| **Artifacts** | `create_artifact`, `list_artifacts`, `list_audit_logs` |
| **GitHub** | `run_github_repo`, `github_post_comment` |
| **Filesystem** | `fs_read_file`, `fs_write_file`, `fs_list_dir`, `fs_delete_file` |
| **S3** | `s3_list_buckets`, `s3_list_objects`, `s3_get_object`, `s3_put_object`, `s3_delete_object`, `s3_head_object` |
| **SSM** | `ssm_get_parameter`, `ssm_put_parameter`, `ssm_delete_parameter`, `ssm_list_parameters` |
| **Secrets Manager** | `sm_get_secret`, `sm_list_secrets` |
| **Lambda** | `lambda_list_functions`, `lambda_invoke` |
| **SQLite** | `sqlite_query`, `sqlite_execute`, `sqlite_schema` |
| **PostgreSQL** | `pg_query`, `pg_execute`, `pg_schema` |
| **MongoDB** | `mongo_find`, `mongo_insert_one`, `mongo_update`, `mongo_delete`, `mongo_aggregate`, `mongo_collections`, `mongo_generate_mongoose` |

Full documentation: [sdk/mcp/README.md](sdk/mcp/README.md)

## GitHub CI Runner

Runs PR test suites inside VaultRun sandboxes triggered by GitHub webhooks.

```bash
GITHUB_TOKEN=ghp_...               \
GITHUB_WEBHOOK_SECRET=your-secret  \
VAULTRUN_BASE_URL=http://vaultrun  \
VAULTRUN_API_KEY=vr_...            \
SLACK_WEBHOOK_URL=https://...      \   # optional
TEAMS_WEBHOOK_URL=https://...      \   # optional
NOTIFY_ON_SUCCESS=false            \   # suppress green noise
CI_TEST_COMMANDS='[["make","test"]]' \ # default
./ci-runner
```

- **Webhook endpoint:** `POST /webhook` (HMAC-SHA256 validated)
- **Health check:** `GET /healthz`
- Results posted as a PR comment + commit status (`vaultrun-ci`)
- Slack Block Kit and Teams Adaptive Card 1.4 payloads

## REST API Reference

All endpoints require `X-API-Key` or `Authorization: Bearer <key>`.

### Sessions

```
POST   /api/v1/sessions          Create a new session
GET    /api/v1/sessions          List active sessions
GET    /api/v1/sessions/:id      Get session details
DELETE /api/v1/sessions/:id      Delete session + container + workspace
```

**Create session body:**
```json
{
  "name": "my-session",
  "image": "python:3.12-slim",
  "network_enabled": false,
  "cpu_limit": 1.0,
  "memory_limit_mb": 512,
  "timeout_seconds": 300
}
```

### Command Execution

```
POST /api/v1/sessions/:id/run     Execute a command
GET  /api/v1/sessions/:id/runs    List runs for a session
GET  /api/v1/runs/:id             Get run details
```

**Execute command body:**
```json
{ "command": "python", "args": ["script.py"], "timeout_seconds": 30 }
```

**Response:**
```json
{
  "id": "uuid",
  "status": "completed",
  "exit_code": 0,
  "stdout": "...",
  "stderr": "",
  "duration_ms": 412
}
```

### File Vault

```
POST /api/v1/sessions/:id/files          Upload file (multipart)
GET  /api/v1/sessions/:id/files          List files
GET  /api/v1/sessions/:id/files/*path    Download file
```

### Audit Logs

```
GET /api/v1/audit?session_id=...    List audit logs
```

### Key Management

```
POST /api/v1/keys    Create API key (requires master key)
GET  /api/v1/keys    List API keys
```

Full OpenAPI spec: [docs/openapi.yaml](docs/openapi.yaml)

## CLI

```bash
export VAULTRUN_API_URL=http://localhost:8080
export VAULTRUN_API_KEY=vr_...

vaultrun session create --image python:3.12-slim --cpu 0.5 --mem 256
vaultrun session list
vaultrun file upload <session-id> ./script.py
vaultrun run <session-id> -- python script.py
vaultrun logs <run-id>
vaultrun session delete <id>
```

## Python SDK

```python
from sandbox_sdk import Client

client = Client("http://localhost:8080", api_key="vr_...")
session = client.create_session(image="python:3.12-slim", memory_limit_mb=256)
client.upload_file(session.id, "script.py", open("script.py", "rb"))
result = client.run(session.id, command="python", args=["script.py"])
print(result.stdout)
client.delete_session(session.id)
```

Install: `pip install ./sdk/python`

## Go SDK

```go
import vaultrun "github.com/nickvd7/vaultrun/sdk/go"

client := vaultrun.New("http://localhost:8080", "vr_...")
session, _ := client.CreateSession(ctx, vaultrun.CreateSessionOptions{Image: "python:3.12-slim"})
client.UploadFile(ctx, session.ID, "script.py", scriptContent)
run, _ := client.Run(ctx, session.ID, vaultrun.RunOptions{Command: "python", Args: []string{"script.py"}})
fmt.Println(*run.Stdout)
```

## Security

See [docs/security.md](docs/security.md) for the full security model.

- No shell execution — commands go through Docker exec API
- Non-root containers with all capabilities dropped
- Network disabled by default
- Path traversal prevention at the workspace layer
- API keys stored as SHA-256 hashes, never in plaintext
- HMAC-signed audit trail for every action
- Rate limiting + security headers on the MCP HTTP transport
- `MCP_AWS_ENABLED=true` explicit opt-in prevents ambient IAM credential activation

## Configuration

All configuration is via environment variables. See [.env.example](.env.example).

**API server:**

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | API server port |
| `DATABASE_URL` | — | Postgres DSN (required) |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `WORKSPACE_BASE_DIR` | `/data/workspaces` | Session workspace root |
| `MASTER_API_KEY` | — | Bootstrap key (use once, then disable) |
| `MAX_FILE_MB` | `100` | Max file upload size |
| `MAX_OUTPUT_MB` | `10` | Max command output size |
| `DOCKER_IDLE_TIMEOUT_MINS` | `30` | Container idle cleanup |

**MCP server** (`MCP_TRANSPORT=http` extras):

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `MCP_AUTH_TOKEN` | — | Bearer token (required for HTTP) |
| `MCP_PORT` | `:8090` | Listen address for HTTP transport |
| `MCP_ALLOWED_ORIGINS` | `*` | CORS origins (comma-separated) |
| `MCP_RATE_LIMIT` | `60` | Requests/min per IP |
| `MCP_FS_ALLOWED_PATHS` | — | Allowed filesystem paths (comma-separated) |
| `MCP_AWS_ENABLED` | `false` | Set `true` to enable all AWS tools |
| `AWS_REGION` | `us-east-1` | AWS region |
| `MCP_SQLITE_PATH` | — | Path to SQLite database file |
| `MCP_PG_DSN` | — | PostgreSQL connection string |
| `MCP_MONGO_URI` | — | MongoDB connection URI |
| `MCP_MONGO_DB` | `test` | MongoDB database name |

## Development

```bash
make test               # unit tests
make test-integration   # integration tests (requires running stack)
make fmt vet            # format + vet
make build              # build API + CLI binaries
make lint               # golangci-lint

# MCP server
go test ./sdk/mcp/...
go build -o vaultrun-mcp ./sdk/mcp/

# CI runner
go test ./cmd/ci-runner/...
go build -o ci-runner ./cmd/ci-runner/
```

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the version history.

## License

Apache 2.0
