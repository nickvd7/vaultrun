# VaultRun

<p align="center">
  <img src="site/brand/lockup.png" alt="VaultRun" width="420">
</p>

[![CI](https://github.com/nickvd7/vaultrun/actions/workflows/ci.yml/badge.svg)](https://github.com/nickvd7/vaultrun/actions/workflows/ci.yml)
[![PyPI](https://img.shields.io/pypi/v/vaultrun-sdk.svg)](https://pypi.org/project/vaultrun-sdk/)
![Go version](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-Apache%202.0-blue)
![Self-hosted](https://img.shields.io/badge/self--hosted-no%20SaaS%2C%20no%20telemetry-success)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen)](https://github.com/nickvd7/vaultrun/pulls)

**Run your AI agents' code in isolated sandboxes on your own infrastructure, and prove to your auditor exactly what they did.** — [vaultrun.dev](https://vaultrun.dev) · [llms.txt](https://vaultrun.dev/llms.txt) · [PyPI](https://pypi.org/project/vaultrun-sdk/)

- **Isolated.** Every agent session gets its own container: no Linux capabilities, read-only root filesystem, seccomp, no network unless you allowlist hosts, and optionally [gVisor](docs/configuration.md#sandbox-runtime-gvisor), a user-space kernel between agent code and your host.
- **Yours.** Self-hosted. No SaaS, no telemetry. Code, data and prompts stay in your network.
- **Provable.** Every session exports as a [signed evidence pack](docs/features/evidence-packs.md) (commands, outputs, files, audit log) with an independent RFC 3161 timestamp. Anyone with the public key verifies it offline; change one byte and it fails.

Agents connect through the REST API, the Python and Go SDKs, or the [MCP server](#mcp-server-71-tools) (Claude, OpenAI, local models).

## See it in 60 seconds

With VaultRun running (see [Quickstart](#quickstart)):

```bash
export VAULTRUN_API_KEY=vr_...   # make bootstrap-key
examples/evidence-demo/demo.sh
```

```
== 2. The agent runs the code it wrote
exit 0: total 200
== 3. The session is exported as a signed evidence pack
Wrote evidence-demo/pack.json
== 4. The auditor verifies it offline: no server, no API key
VALID signature (ed25519, key 9d7c7dece222cc59)
Timestamp 2026-10-29 08:20:16 UTC by CN=DigiCert SHA256 RSA4096 Timestamp Responder 2026 1 (RFC 3161)   # with EVIDENCE_TSA_URL set
Session   "invoice-agent" · image python:3.12-slim · network off
Runs      1 (1 exit 0)
Audit     3 entries (scope all), 3 verified by the server at export
== 5. Someone changes one character of the recorded output
Error: NOT VALID: evidence signature does not match
```

## Quickstart

**Prerequisites:** Docker, Docker Compose, `curl` and `jq` (Go 1.25+ only if you build outside Docker)

```bash
git clone https://github.com/nickvd7/vaultrun
cd vaultrun
cp .env.example .env        # set MASTER_API_KEY (openssl rand -hex 32); make up generates the Postgres, Redis and audit secrets
make gvisor                 # optional, Linux: install gVisor; make up then runs sandboxes under it
make up                     # detects the Docker GID, then starts API + Postgres + Redis + dashboard
make bootstrap-key          # prints your first vr_... API key
curl http://localhost:8080/health
```

Open `http://localhost:3000` for the dashboard. The API is secure by default; the quickstart `.env` sets `VAULTRUN_INSECURE_DEV=true` for local use, which you remove in production ([production hardening](docs/production-hardening.md)).

On Linux, your user must be allowed to run `docker` without `sudo`: `sudo usermod -aG docker $USER`, then **log out and in again** (or run `newgrp docker`) before `make up`; the new group is not picked up by shells that are already open. Running `make up` through `sudo` works too, but then `.env` and the workspace files belong to root.

## What's included

| Component | Description |
|---|---|
| **API server** (`cmd/api`) | Gin-based REST API — sessions, runs, files, audit |
| **CLI** (`cmd/cli`) | `vaultrun` command-line tool |
| **MCP server** (`sdk/mcp`) | 71-tool MCP server (stdio + HTTP, +6 Flowd when enabled) |
| **CI runner** (`cmd/ci-runner`) | GitHub webhook → sandbox CI + Slack/Teams notify |
| **Local gateway** (`cmd/local`) | OpenAI-compat action plane for Ollama / LM Studio / vLLM → VaultRun tools ([docs](docs/local-gateway.md) · [recipes](examples/local-gateway/)) |
| **Dashboard** (`apps/frontend`) | Next.js management UI |
| **Go SDK** (`sdk/go`) | Typed Go client |
| **Python SDK** (`sdk/python`) | Python client — [`pip install vaultrun-sdk`](https://pypi.org/project/vaultrun-sdk/) |

## Features

| | |
|---|---|
| **Evidence packs** | Signed record of a session, verified offline with `vaultrun evidence verify`; external RFC 3161 timestamps; bulk export per period ([docs](docs/features/evidence-packs.md)) |
| **Kernel isolation** | `DOCKER_RUNTIME=runsc` runs every sandbox under gVisor (`make gvisor` installs it; `make up` picks it up; production starts require it or an explicit opt-out); tested in CI end to end ([docs](docs/configuration.md#sandbox-runtime-gvisor)) |
| **Network allowlist** | `allowed_hosts` enforced with per-session iptables rules; no network by default ([docs](docs/security.md)) |
| **Tamper-evident audit log** | Hash-chained, HMAC-signed, verified as a whole ([docs](docs/security.md)) |
| **Session replay** | Checkpoints, restore and fork of a session's workspace ([docs](docs/features/session-replay.md)) |
| **Browser automation** | Headless Chromium (Playwright) with 8 MCP tools and SSRF protection ([docs](docs/browser-automation.md)) |
| **Cost intelligence** | Per-session cost tracking, budgets and alerts ([docs](docs/features/cost-intelligence.md)) |
| **Natural-language policies** | "Allow github.com and pypi.org, max 2 CPU" → OPA policy, network rules, limits ([docs](docs/natural-language-policy.md)) |
| **Templates and multi-agent** | Pre-configured environments; several agents in one session ([templates](docs/features/session-templates.md) · [collaboration](docs/features/multi-agent-collaboration.md)) |

Security testing: [report](docs/security-testing-report.md). Release notes: [CHANGELOG.md](CHANGELOG.md).

## VaultRun Enterprise

For teams that answer to auditors, regulators or their customers' security reviews.

| | Core (Apache 2.0) | Enterprise |
|---|:-:|:-:|
| Sandboxes, gVisor isolation, network allowlist, MCP server | ✓ | ✓ |
| Signed evidence packs, external timestamps, offline verifier | ✓ | ✓ |
| **Compliance report**: every pack verified and organised by control (integrity, independent time, audit log, traceability, isolation, network, accountability), with exceptions, findings and an evidence index; HTML/PDF and JSON | | ✓ |
| **Single sign-on**: OIDC and SAML 2.0 (Okta, Entra ID, Google, AD FS) | | ✓ |
| **Support** with agreed response times, security patches for supported releases | | ✓ |
| **Audit help**: walking your auditor through the evidence | | ✓ |

Enterprise is free to evaluate for development and testing. A pilot installs it in your environment and delivers your first compliance report. Contact: [vaultrun.dev/#enterprise](https://vaultrun.dev/#enterprise) · **mail@030.dev**

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
│  • Signed audit log + evidence packs                         │
│  • CPU / memory / timeout limits per session                 │
│  • Network disabled by default, optional gVisor kernel       │
└──────────────────────────────────────────────────────────────┘
```

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

## MCP Server (71 tools)

The MCP server exposes every VaultRun capability as a Model Context Protocol tool, including browser automation, memory, verification, task control, and optional Flowd integration (+6 tools).
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
from vaultrun_sdk import Client

client = Client("http://localhost:8080", api_key="vr_...")
session = client.create_session(image="python:3.12-slim", memory_limit_mb=256)
client.upload_file(session.id, "script.py", open("script.py", "rb"))
result = client.run(session.id, command="python", args=["script.py"])
print(result.stdout)
client.delete_session(session.id)
```

Install: [`pip install vaultrun-sdk`](https://pypi.org/project/vaultrun-sdk/)

## Go SDK

```go
import vaultrun "github.com/nickvd7/vaultrun/sdk/go"

client := vaultrun.New("http://localhost:8080", "vr_...")
session, _ := client.CreateSession(ctx, vaultrun.CreateSessionOptions{Image: "python:3.12-slim"})
client.UploadFile(ctx, session.ID, "script.py", scriptContent)
run, _ := client.Run(ctx, session.ID, vaultrun.RunOptions{Command: "python", Args: []string{"script.py"}})
fmt.Println(*run.Stdout)
```

## SSO / Authentication

VaultRun supports three authentication methods:

| Method | How | Use case | Edition |
|---|---|---|---|
| **API key** | `X-API-Key: vr_…` header | Agents, SDKs, CI | Core (Apache 2.0) |
| **OIDC** | Browser redirect to IdP → session cookie | Dashboard users via Okta/Azure AD/Google | Enterprise |
| **SAML 2.0** | Browser redirect to IdP → session cookie | Enterprise IdPs (Okta, AD FS, OneLogin) | Enterprise |

SSO logins auto-provision a VaultRun API key and issue a signed session cookie. OIDC and SAML do not grant master-key privileges.

SSO ships as **VaultRun Enterprise** in a separate repository and is compiled into the API server as an overlay (`go build -tags enterprise`). A core build refuses to start when `OIDC_*`/`SAML_*` env vars are set, so misconfigured deployments fail loudly.

### How to get Enterprise

See [VaultRun Enterprise](#vaultrun-enterprise) for what it adds. Pick a path on [vaultrun.dev/#enterprise](https://vaultrun.dev/#enterprise) — every request is emailed to **mail@030.dev**:

1. **Evaluate** — free for development and testing; request evaluation access
2. **License** — production SSO; include org, IdP, instances/seats, and timeline
3. **Talk to us** — procurement, support, or custom terms

See [docs/sso-setup.md](docs/sso-setup.md) for a step-by-step setup guide (IdP registration, certificates, testing, troubleshooting), or [docs/configuration.md](docs/configuration.md#sso--oidc--openid-connect) for the environment variable reference.

## Security

See [docs/security.md](docs/security.md) for the full security model.

- No shell execution — commands go through Docker exec API
- Non-root containers with all capabilities dropped
- Network disabled by default; per-session iptables allowlist when enabled
- Path traversal prevention at the workspace layer
- API keys stored as SHA-256 hashes, never in plaintext
- HMAC-signed audit trail for every action
- Rate limiting + security headers on the MCP HTTP transport
- `MCP_AWS_ENABLED=true` explicit opt-in prevents ambient IAM credential activation
- OIDC: PKCE + state validation; SAML: XMLDSig signature verification
- Session cookies: `HttpOnly`, `Secure`, HS256 signed

## Configuration

All configuration is via environment variables. See [.env.example](.env.example) and [docs/configuration.md](docs/configuration.md).

**API server (required):**

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | API server port |
| `DATABASE_URL` | — | Postgres DSN (required) |
| `MASTER_API_KEY` | — | Bootstrap key (use once, then disable) |

**SSO (optional):**

| Variable | Description |
|---|---|
| `OIDC_ISSUER_URL` | Enable OIDC — e.g. `https://accounts.google.com` |
| `SAML_IDP_METADATA_URL` | Enable SAML — URL of the IdP's metadata XML |
| `SSO_SESSION_SECRET` | Required when SSO is enabled — `openssl rand -hex 32` |

**Multi-region (optional):**

| Variable | Description |
|---|---|
| `REGION` | Region tag included in `/health` responses |
| `DATABASE_READ_URL` | Read-replica DSN for list/get queries |

**MCP server** (`MCP_TRANSPORT=http` extras):

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `MCP_AUTH_TOKEN` | — | Bearer token (required for HTTP) |
| `MCP_PORT` | `:8090` | Listen address for HTTP transport |
| `MCP_SQLITE_PATH` | — | Path to SQLite database file |
| `MCP_PG_DSN` | — | PostgreSQL connection string |
| `MCP_MONGO_URI` | — | MongoDB connection URI |

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

## Support & feedback

| Topic | Channel |
|-------|---------|
| **Bugs & feature requests** (open core) | [GitHub Issues](https://github.com/nickvd7/vaultrun/issues/new/choose) |
| **Security vulnerabilities** | Private only — see [SECURITY.md](SECURITY.md) |
| **Enterprise / SSO / licensing** | [vaultrun.dev/#contact](https://vaultrun.dev/#contact) → **mail@030.dev** |

Please use the issue templates (version, component, repro steps). Do not put secrets in issues.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the version history.

## License

VaultRun is open core:

- Everything in this repository is licensed under **Apache 2.0** — see [LICENSE](LICENSE).
- **VaultRun Enterprise** (compliance report, SSO: OIDC + SAML 2.0) lives in a separate repository under the VaultRun Enterprise License.

Enterprise licensing & support: **[vaultrun.dev/#enterprise](https://vaultrun.dev/#enterprise)** → emails **mail@030.dev**
