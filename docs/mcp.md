# MCP Server — Deep-Dive Guide

This guide covers everything needed to run the VaultRun MCP server in production:
transport selection, TLS, authentication, rate limiting, and per-tool examples.

## Protocol versions

VaultRun MCP is built on the **official Go MCP SDK** (`github.com/modelcontextprotocol/go-sdk` v1.7.0+)
and speaks **MCP `2026-07-28`** (stateless Streamable HTTP) with fallback for older clients.

| Feature | Status |
|---|---|
| Official Go SDK transport | stdio + HTTP (`Stateless`, JSON responses) |
| Tasks extension | `async=true` → `taskId`; poll/update/cancel via tools `get_task` / `update_task` / `cancel_task` (also custom methods `tasks/*`); `input_required` via `inputRequests`/`inputResponses`; optional `confirm=true` |
| Verify checkpoints | MCP `verify_checkpoint` + `POST /api/v1/verify` (`exit_code_zero`, `stdout_contains`, `file_exists`) |
| Agent memory | `memory_set` / `memory_get` / `memory_list` / `memory_delete` → `.vaultrun/memory/` in session workspace |
| MCP Apps | `ui://vaultrun/session-panel` (+ tool UI meta); disable with `MCP_APPS_ENABLED=false` |
| OAuth / EMA (server) | PRM at `/.well-known/oauth-protected-resource` when `MCP_OAUTH_ISSUERS` is set; optional introspection |

Tool arguments accept normal JSON types (booleans, numbers, objects, arrays); the server coerces them for handlers. Nested values such as `env` or command `args` may be sent as JSON objects/arrays or as JSON strings.

Application state (sandbox sessions) is **not** protocol state: tools return an
explicit `session_id` that clients pass on later calls.

### Tasks env

| Variable | Purpose |
|---|---|
| `MCP_TASK_TTL_SECONDS` | How long finished tasks stay available (default `1800`) |
| `MCP_TASK_MAX_AGE_SECONDS` | Max age for working tasks before forced fail (default `7200`) |
| `MCP_TASK_MAX_INFLIGHT` | Cap on concurrent working tasks (default `64`) |
| `MCP_REDIS_ADDR` / `REDIS_ADDR` | When set and reachable, task metadata is stored in Redis (durable + multi-instance). Falls back to in-memory on ping failure. |
| `MCP_REDIS_PASSWORD` / `REDIS_PASSWORD` | Redis password |
| `MCP_REDIS_DB` / `REDIS_DB` | Redis DB index (default `0`) |

Redis keys: `mcp:task:<taskId>`, `mcp:tasks:inflight` (SET), pub/sub `mcp:tasks:cancel` for cross-instance cancel.

### Tasks observability

When the MCP HTTP server is running, scrape Prometheus metrics at `GET /metrics`
(same bind as the MCP port). Protect with `MCP_METRICS_TOKEN` or `METRICS_TOKEN`
(`Authorization: Bearer …`); if unset the endpoint is open — restrict via network policy.

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `vaultrun_mcp_tasks_started_total` | counter | `tool`, `backend` | Task created |
| `vaultrun_mcp_tasks_terminal_total` | counter | `status`, `backend` | Reached completed/failed/cancelled |
| `vaultrun_mcp_tasks_cancelled_total` | counter | | Explicit cancel that transitioned a working task |
| `vaultrun_mcp_tasks_input_required_total` | counter | | Entered `input_required` |
| `vaultrun_mcp_tasks_inflight` | gauge | `backend` | Current non-terminal tasks |
| `vaultrun_mcp_tasks_ttl_evicted_total` | counter | | Removed by idle TTL (memory) |
| `vaultrun_mcp_tasks_max_age_failed_total` | counter | | Forced failed by max age |
| `vaultrun_mcp_tasks_redis_fallback_total` | counter | | Redis create failed → memory |
| `vaultrun_mcp_tasks_inflight_rejected_total` | counter | | Rejected by max concurrent |

Structured logs (`vaultrun-mcp: task …`) emit at create, terminal transitions, cancel,
`input_required`, TTL/max-age, and Redis fallback. `backend` is `memory` or `redis`.

### Tasks and the official Go SDK

VaultRun depends on [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) **v1.7.0** for transport and tool bridging. That SDK release **does not** include first-class Tasks APIs yet (no built-in `tasks/get` / `tasks/update` / `tasks/cancel` helpers or `CreateTaskResult` types).

Until upstream ships Tasks:

- Capability advertisement: `io.modelcontextprotocol/tasks` via `ServerCapabilities.AddExtension`
- Wire methods: registered with `AddReceivingCustomMethod` (`tasks/get`, `tasks/update`, `tasks/cancel`)
- **Tool wrappers (recommended for Cursor / Claude / tools-only hosts):** `get_task`, `update_task`, `cancel_task`
- Async opt-in: VaultRun `async=true` on taskable tools (plus optional `confirm=true` → `input_required`)
- Persistence / caps: VaultRun `taskStore` (memory or Redis) — unchanged when migrating

Migration notes live in `sdk/mcp/tasks_sdk_compat.go`. Re-check on each `go-sdk` upgrade; latest v1.7.0 still has no first-class Tasks. Prefer adopting SDK-native helpers when available; keep VaultRun’s store, security caps, and elicitation behavior.

### OAuth / EMA env (HTTP)

| Variable | Purpose |
|---|---|
| `MCP_AUTH_TOKEN` | Static bearer (always supported) |
| `MCP_OAUTH_ISSUERS` | Comma-separated authorization server issuer URLs → enables PRM |
| `MCP_PUBLIC_BASE_URL` / `MCP_RESOURCE_URL` | Public resource URL advertised in PRM |
| `MCP_OAUTH_SCOPES` | Scopes listed in PRM (default `mcp`) |
| `MCP_OAUTH_INTROSPECTION_URL` | RFC 7662 introspection endpoint |
| `MCP_OAUTH_INTROSPECTION_CLIENT_ID` / `_SECRET` | Introspection client credentials |

EMA clients use the IdP + MCP auth-server flow on the **client** side; this server
advertises PRM and validates tokens (static and/or introspection).

### MCP Apps

Hosts that support the MCP Apps extension (`io.modelcontextprotocol/ui`) can
render lightweight HTML panels shipped by VaultRun:

| Resource URI | Attached tools |
|---|---|
| `ui://vaultrun/session-panel` | `list_sessions`, `get_session`, `create_session` |
| `ui://vaultrun/run-panel` | `run_command`, `get_run` |
| `ui://vaultrun/artifacts-panel` | `list_artifacts` |

MIME type: `text/html;profile=mcp-app`. Disable all Apps with `MCP_APPS_ENABLED=false`.

Panels remind hosts that VaultRun is **stateless at the MCP layer**: clients must
pass explicit `session_id` handles between tool calls. Hosts without Apps support
ignore the resources and keep using normal tool text.

EMA (Enterprise Managed Authorization) is a **client** concern. Server-side you
only need PRM + token validation (see OAuth env above); Apps panels do not
perform auth themselves.

---

## Transport selection

| Transport | Use case |
|---|---|
| **stdio** (default) | Claude Desktop, Claude Code — server runs as a subprocess |
| **http** | OpenAI, OpenRouter, custom agents — server runs as a long-lived HTTP service |

Set `MCP_TRANSPORT=http` to activate the HTTP transport. Omit it (or set `stdio`) for the default.

---

## stdio transport

The stdio transport is the simplest option and requires no extra configuration beyond
the two required env vars.

```bash
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_yourkeyhere \
./vaultrun-mcp
```

The server reads newline-delimited JSON-RPC 2.0 requests from stdin and writes responses to stdout.
One request per line, one response per line.

Modern clients may probe with `server/discover` first; legacy clients may still
send `initialize` / `initialized`. Both are supported.

### Claude Desktop

`~/.config/claude/claude_desktop_config.json` (macOS: `~/Library/Application Support/Claude/`):

```json
{
  "mcpServers": {
    "vaultrun": {
      "command": "/usr/local/bin/vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_yourkeyhere"
      }
    }
  }
}
```

### Claude Code

```bash
claude mcp add vaultrun /usr/local/bin/vaultrun-mcp \
  -e VAULTRUN_BASE_URL=http://localhost:8080 \
  -e VAULTRUN_API_KEY=vr_yourkeyhere
```

Or in `.claude/settings.json`:

```json
{
  "mcpServers": {
    "vaultrun": {
      "type": "stdio",
      "command": "/usr/local/bin/vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_yourkeyhere"
      }
    }
  }
}
```

---

## HTTP transport

```bash
MCP_TRANSPORT=http \
MCP_AUTH_TOKEN=your-secret-token \
MCP_PORT=:8090 \
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_yourkeyhere \
./vaultrun-mcp
```

`MCP_AUTH_TOKEN` is **required** — the server refuses to start without it when `MCP_TRANSPORT=http`.

### Endpoints

| Method | Path | Description |
|---|---|---|
| `POST` | `/mcp` | JSON-RPC 2.0 request/response (Streamable HTTP) |
| `GET` | `/sse` | Legacy HTTP+SSE stub (deprecated in MCP 2026-07-28; prefer `POST /mcp`) |
| `GET` | `/` | Server info JSON (includes `supported_versions`) |
| `GET` | `/healthz` | Health check — returns `{"ok":true}` |
| `GET` | `/metrics` | Prometheus metrics (optional `MCP_METRICS_TOKEN` / `METRICS_TOKEN`) |

### Authentication

Every `POST /mcp` request must include:

```
Authorization: Bearer your-secret-token
```

Missing or wrong token → `401 Unauthorized`.

### Modern (2026-07-28) request headers

When speaking `2026-07-28`, clients **must** send:

| Header | Value |
|---|---|
| `MCP-Protocol-Version` | `2026-07-28` (must match `params._meta["io.modelcontextprotocol/protocolVersion"]`) |
| `Mcp-Method` | JSON-RPC `method` (e.g. `tools/list`, `tools/call`) |
| `Mcp-Name` | Required for `tools/call` — must match `params.name` |

Header mismatches → `400` with JSON-RPC error `-32020`. Unknown methods on the
modern path → `404` with `-32601`. Legacy clients that omit these headers keep
working (HTTP `200` + JSON-RPC body as before).

### Test with curl

```bash
# Legacy tools/list (still supported)
curl -s -X POST http://localhost:8090/mcp \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  | jq '.result.tools | length'
# → 53 (59 with MCP_FLOWD_ENABLED=true)

# Modern server/discover
curl -s -X POST http://localhost:8090/mcp \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: server/discover" \
  -d '{
    "jsonrpc":"2.0","id":1,"method":"server/discover",
    "params":{"_meta":{
      "io.modelcontextprotocol/protocolVersion":"2026-07-28",
      "io.modelcontextprotocol/clientInfo":{"name":"curl","version":"0"},
      "io.modelcontextprotocol/clientCapabilities":{}
    }}
  }' | jq '.result.supportedVersions'

# Modern tools/call
curl -s -X POST http://localhost:8090/mcp \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: tools/call" \
  -H "Mcp-Name: list_sessions" \
  -d '{
    "jsonrpc":"2.0","id":2,"method":"tools/call",
    "params":{
      "name":"list_sessions","arguments":{},
      "_meta":{
        "io.modelcontextprotocol/protocolVersion":"2026-07-28",
        "io.modelcontextprotocol/clientCapabilities":{}
      }
    }
  }' | jq .
```

---

## TLS

### Option A — Let's Encrypt (automatic)

```bash
MCP_TRANSPORT=http \
MCP_AUTH_TOKEN=secret \
MCP_ACME_DOMAIN=mcp.example.com \
MCP_ACME_CACHE_DIR=/var/cache/vaultrun-mcp/acme \
MCP_ACME_EMAIL=admin@example.com \
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_... \
./vaultrun-mcp
```

The server will automatically obtain and renew a Let's Encrypt certificate.
Port 443 must be reachable from the internet.

### Option B — Static certificate

```bash
MCP_TRANSPORT=http \
MCP_AUTH_TOKEN=secret \
MCP_TLS_CERT=/etc/ssl/certs/mcp.crt \
MCP_TLS_KEY=/etc/ssl/private/mcp.key \
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_... \
./vaultrun-mcp
```

### Option C — Reverse proxy (nginx / Caddy)

Run the MCP server on a non-public port and terminate TLS at the proxy:

```nginx
# nginx example
server {
    listen 443 ssl;
    server_name mcp.example.com;

    ssl_certificate     /etc/ssl/certs/mcp.crt;
    ssl_certificate_key /etc/ssl/private/mcp.key;

    location / {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Authorization $http_authorization;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

Set `MCP_TRUSTED_PROXIES=127.0.0.1` so rate limiting uses the real client IP.

---

## Rate limiting

Three tiers apply in addition to a global per-IP limit:

| Tier | Limit | Tools |
|---|---|---|
| **Heavy** | 10 req/min | `create_session`, `run_command`, `run_github_repo`, `pull_image`, `create_snapshot`, `create_artifact`, `lambda_invoke` |
| **Write** | 30 req/min | `upload_file`, `delete_file`, `delete_session`, `github_post_comment`, `fs_write_file`, `fs_delete_file`, `s3_put_object`, `s3_delete_object`, `ssm_put_parameter`, `ssm_delete_parameter`, `sm_get_secret`, `sqlite_execute`, `pg_execute`, `mongo_insert_one`, `mongo_update`, `mongo_delete` |
| **Read** | 60 req/min (global) | All other tools |

Set `MCP_RATE_LIMIT=N` to change the global limit (default 60/min). The heavy and write tiers
are scaled proportionally (heavy = global/6, write = global/2).

---

## Optional feature groups

### Filesystem tools

```bash
MCP_FS_ALLOWED_PATHS=/data/reports,/home/agent/workspace
```

All four filesystem tools (`fs_read_file`, `fs_write_file`, `fs_list_dir`, `fs_delete_file`) are
disabled when `MCP_FS_ALLOWED_PATHS` is not set. Paths are validated against the allowlist at
request time with symlink resolution.

### AWS tools

```bash
MCP_AWS_ENABLED=true
AWS_REGION=eu-west-1
# Static credentials (optional — falls back to IAM role)
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
# MinIO / LocalStack
AWS_ENDPOINT_URL=http://localhost:9000
MCP_S3_FORCE_PATH_STYLE=true
```

Explicit opt-in via `MCP_AWS_ENABLED=true` is required. This prevents accidentally exposing
AWS operations in EC2/ECS environments that have ambient instance-role credentials.

### Database tools

```bash
# SQLite — any combination can be enabled simultaneously
MCP_SQLITE_PATH=/data/app.db

# PostgreSQL
MCP_PG_DSN=postgres://user:pass@localhost:5432/mydb

# MongoDB
MCP_MONGO_URI=mongodb://localhost:27017
MCP_MONGO_DB=myapp
```

---

## Per-tool examples

### create_session

```json
{
  "jsonrpc": "2.0", "id": 1, "method": "tools/call",
  "params": {
    "name": "create_session",
    "arguments": {
      "image": "node:20-slim",
      "network_enabled": "true",
      "cpu_limit": "0.5",
      "memory_limit_mb": "512",
      "timeout_seconds": "600"
    }
  }
}
```

### run_command

```json
{
  "jsonrpc": "2.0", "id": 2, "method": "tools/call",
  "params": {
    "name": "run_command",
    "arguments": {
      "session_id": "sess_abc123",
      "command": "python",
      "args": ["script.py"],
      "working_dir": "/workspace",
      "timeout_seconds": "30"
    }
  }
}
```

### upload_file

```json
{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": {
    "name": "upload_file",
    "arguments": {
      "session_id": "sess_abc123",
      "path": "script.py",
      "content": "print('hello')\n"
    }
  }
}
```

### run_github_repo

```json
{
  "jsonrpc": "2.0", "id": 4, "method": "tools/call",
  "params": {
    "name": "run_github_repo",
    "arguments": {
      "owner": "acme",
      "repo": "backend",
      "branch": "main",
      "command": "make",
      "args": ["test"]
    }
  }
}
```

Requires `GITHUB_TOKEN`. The token is passed via `http.extraheader` — it never appears
in any git remote URL or log output.

### sqlite_query

```json
{
  "jsonrpc": "2.0", "id": 5, "method": "tools/call",
  "params": {
    "name": "sqlite_query",
    "arguments": {
      "query": "SELECT id, name, created_at FROM users ORDER BY created_at DESC LIMIT 10"
    }
  }
}
```

### mongo_find

```json
{
  "jsonrpc": "2.0", "id": 6, "method": "tools/call",
  "params": {
    "name": "mongo_find",
    "arguments": {
      "collection": "orders",
      "filter": "{\"status\": \"pending\", \"total\": {\"$gt\": 100}}",
      "limit": "25"
    }
  }
}
```

### mongo_aggregate

```json
{
  "jsonrpc": "2.0", "id": 7, "method": "tools/call",
  "params": {
    "name": "mongo_aggregate",
    "arguments": {
      "collection": "orders",
      "pipeline": "[{\"$group\":{\"_id\":\"$status\",\"count\":{\"$sum\":1},\"total\":{\"$sum\":\"$amount\"}}}]"
    }
  }
}
```

### mongo_generate_mongoose

```json
{
  "jsonrpc": "2.0", "id": 8, "method": "tools/call",
  "params": {
    "name": "mongo_generate_mongoose",
    "arguments": {
      "collection": "products"
    }
  }
}
```

Example output:
```javascript
// Auto-generated Mongoose schema for collection "products"
// Sampled from 50 document(s)

const mongoose = require('mongoose');
const { Schema } = mongoose;

const productsSchema = new Schema({
  name: { type: String },
  price: { type: Number },
  inStock: { type: Boolean },
  tags: { type: [Schema.Types.Mixed] },
  meta: { type: Schema.Types.Mixed }, // nullable
}, { timestamps: true });

module.exports = mongoose.model('Products', productsSchema);
```

### s3_put_object

```json
{
  "jsonrpc": "2.0", "id": 9, "method": "tools/call",
  "params": {
    "name": "s3_put_object",
    "arguments": {
      "bucket": "my-bucket",
      "key": "reports/2026-06-05.json",
      "body": "{\"total\": 42}",
      "content_type": "application/json"
    }
  }
}
```

### lambda_invoke

```json
{
  "jsonrpc": "2.0", "id": 10, "method": "tools/call",
  "params": {
    "name": "lambda_invoke",
    "arguments": {
      "function_name": "my-processor",
      "payload": "{\"key\": \"value\"}",
      "invocation_type": "RequestResponse"
    }
  }
}
```

---

## Flowd integration (optional)

Local workflow automation via [Flowd](https://flowd.net). Requires `flowctl` on the same host as `vaultrun-mcp`.

```bash
MCP_FLOWD_ENABLED=true \
FLOWCTL_PATH=flowctl \
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_yourkeyhere \
./vaultrun-mcp
```

Tools: `flowd_list_suggestions`, `flowd_explain_suggestion`, `flowd_approve_suggestion`, `flowd_list_patterns`, `flowd_stats`, `flowd_undo_run`.

Full guide: [flowd-integration.md](flowd-integration.md) · Companion page: https://vaultrun.dev/flowd.html

---

## Security checklist for production

- [ ] `MCP_AUTH_TOKEN` is a long random value (32+ chars): `openssl rand -hex 32`
- [ ] TLS enabled (Let's Encrypt or static cert or reverse proxy)
- [ ] `MCP_ALLOWED_ORIGINS` set to specific origin(s), not `*`
- [ ] `MCP_TRUSTED_PROXIES` set when running behind a reverse proxy
- [ ] `MCP_AWS_ENABLED` only set if AWS tools are needed
- [ ] `MCP_FLOWD_ENABLED` only set if Flowd tools are needed (same host as flowctl)
- [ ] `MCP_FS_ALLOWED_PATHS` scoped to the minimum required directories
- [ ] VaultRun API key is a scoped key (not the master key): `make bootstrap-key`
- [ ] `GITHUB_TOKEN` has minimum scopes (`repo` read or `public_repo` for public repos only)
- [ ] Rate limits tuned for expected traffic (`MCP_RATE_LIMIT`)
- [ ] Log file configured and rotated (`VAULTRUN_LOG_FILE`)
