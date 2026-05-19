# VaultRun

**Self-hosted secure runtime for AI agents.**

VaultRun lets AI agents safely execute tools, run code, access scoped files, and generate artifacts inside isolated Docker sandboxes running on your own infrastructure. No external SaaS. No data leaving your network.

```
┌─────────────────────────────────────────────────────┐
│  Your AI Agent                                      │
│                                                     │
│  result = client.run(session_id,                    │
│      command="python", args=["analyze.py"])         │
│                                                     │
│  print(result.stdout)  # safe, isolated execution   │
└─────────────────────────────────────────────────────┘
         │
         ▼ API Key
┌─────────────────────────────────────────────────────┐
│  VaultRun API  (your server)                        │
│                                                     │
│  • Isolated Docker container per session            │
│  • No shell injection — exec API only               │
│  • Workspace isolation + path traversal prevention  │
│  • Full audit trail                                 │
│  • CPU / memory / timeout limits                    │
│  • Network disabled by default                      │
└─────────────────────────────────────────────────────┘
```

## Quickstart

**Prerequisites:** Docker, Docker Compose, Go 1.23+

```bash
# 1. Clone and configure
git clone https://github.com/nickvd7/vaultrun
cd vaultrun
cp .env.example .env

# 2. Edit .env — set MASTER_API_KEY to something strong
vim .env

# 3. Start all services
make up

# 4. Create your first API key
make bootstrap-key

# 5. Test it
export VAULTRUN_API_KEY=vr_...   # key from step 4
curl http://localhost:8080/health
```

## Architecture

```
vaultrun/
├── cmd/
│   ├── api/          Go API server (Gin)
│   └── cli/          Go CLI (vaultrun)
├── internal/
│   ├── auth/         API key hashing + validation
│   ├── audit/        Audit event logger
│   ├── docker/       Docker container + exec management
│   ├── workspace/    File vault with path traversal prevention
│   ├── runner/       Command execution orchestration
│   ├── db/           Postgres queries (sqlx)
│   ├── policy/       Pluggable policy hook (noop MVP)
│   └── config/       Environment-based configuration
├── apps/
│   └── frontend/     Next.js dashboard (React + Tailwind)
├── sdk/
│   ├── go/           Go SDK
│   └── python/       Python SDK
├── migrations/       SQL migrations (golang-migrate)
├── deployments/      Docker Compose + Dockerfiles
├── docs/             Architecture, security, roadmap
└── examples/         Usage examples
```

## API Reference

All endpoints require `X-API-Key` or `Authorization: Bearer <key>` header.

### Sessions

```
POST   /api/v1/sessions          Create a new session
GET    /api/v1/sessions          List active sessions
GET    /api/v1/sessions/:id      Get session details
DELETE /api/v1/sessions/:id      Delete session + container + workspace
```

**Create session:**
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

**Execute command:**
```json
{
  "command": "python",
  "args": ["script.py"],
  "timeout_seconds": 30
}
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

## CLI Usage

```bash
# Check connectivity
vaultrun up

# Session management
vaultrun session create --image python:3.12-slim --cpu 0.5 --mem 256
vaultrun session list
vaultrun session get <id>
vaultrun session delete <id>

# File operations
vaultrun file upload <session-id> ./script.py
vaultrun file upload <session-id> ./data.csv --path data/input.csv
vaultrun file download <session-id> /output.json
vaultrun file list <session-id>

# Execute commands
vaultrun run <session-id> -- python script.py
vaultrun run <session-id> -- ls -la /workspace
vaultrun run <session-id> --timeout 60 -- python train.py

# Get logs
vaultrun logs <run-id>
```

Set `VAULTRUN_API_URL` and `VAULTRUN_API_KEY` to avoid passing them every time.

## Python SDK

```python
from sandbox_sdk import Client

client = Client("http://localhost:8080", api_key="vr_...")

# Create session
session = client.create_session(
    image="python:3.12-slim",
    cpu_limit=0.5,
    memory_limit_mb=256,
)

# Upload file
client.upload_file(session.id, "script.py", open("script.py", "rb"))

# Execute
result = client.run(
    session.id,
    command="python",
    args=["script.py"],
    timeout_seconds=30,
)

print(result.stdout)
print(f"Exit code: {result.exit_code}")

# Clean up
client.delete_session(session.id)
```

Install: `pip install ./sdk/python`

## Go SDK

```go
import vaultrun "github.com/nickvd7/vaultrun/sdk/go"

client := vaultrun.New("http://localhost:8080", "vr_...")

session, _ := client.CreateSession(ctx, vaultrun.CreateSessionOptions{
    Image:         "python:3.12-slim",
    MemoryLimitMB: 256,
})

client.UploadFile(ctx, session.ID, "script.py", scriptContent)

run, _ := client.Run(ctx, session.ID, vaultrun.RunOptions{
    Command:        "python",
    Args:           []string{"script.py"},
    TimeoutSeconds: 30,
})

fmt.Println(*run.Stdout)
```

## Dashboard

Open `http://localhost:3000` to access the React dashboard.

- **Sessions page** — create / list / delete sessions
- **Session detail** — run commands, upload/download files, view audit logs
- **Audit logs** — searchable, filterable event log

## Security

See [docs/security.md](docs/security.md) for the full security model.

Key principles:
- No shell execution — commands use Docker exec API directly
- Non-root containers with all capabilities dropped
- Network disabled by default
- Path traversal prevention at the workspace layer
- API keys stored as SHA-256 hashes
- Full audit trail for every action

## Development

```bash
# Run tests
make test

# Run integration tests (requires running stack)
make test-integration

# Format + vet
make fmt vet

# Build binaries
make build
```

## Configuration

All configuration is via environment variables. See [.env.example](.env.example).

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | API server port |
| `DATABASE_URL` | — | Postgres DSN |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `WORKSPACE_BASE_DIR` | `/data/workspaces` | Session workspace root |
| `MASTER_API_KEY` | — | Bootstrap key (use once, then disable) |
| `MAX_FILE_MB` | `100` | Max file upload size |
| `MAX_OUTPUT_MB` | `10` | Max command output size |
| `DOCKER_IDLE_TIMEOUT_MINS` | `30` | Container idle cleanup |

## License

Apache 2.0