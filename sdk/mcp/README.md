# VaultRun MCP Server

Model Context Protocol server that exposes all VaultRun capabilities as tools.
Works with Claude Desktop, Claude Code, OpenAI, OpenRouter, and any MCP-compatible platform.

**53+ tools · stdio + HTTP transports · AWS · databases · GitHub CI · Flowd (opt-in)**

## Build

```bash
go build -o vaultrun-mcp ./sdk/mcp/
# or install globally:
go install github.com/nickvd7/vaultrun/sdk/mcp@latest
```

## Quick start

### Claude Desktop / Claude Code (stdio)

Add to `~/.config/claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "vaultrun": {
      "command": "/path/to/vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_yourkeyhere"
      }
    }
  }
}
```

Or via Claude Code CLI:

```bash
claude mcp add vaultrun /path/to/vaultrun-mcp \
  -e VAULTRUN_BASE_URL=http://localhost:8080 \
  -e VAULTRUN_API_KEY=vr_yourkeyhere
```

### OpenAI / OpenRouter / any HTTP client

```bash
MCP_TRANSPORT=http \
MCP_AUTH_TOKEN=your-secret-token \
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_yourkeyhere \
./vaultrun-mcp

# Endpoint: POST http://localhost:8090/mcp
# Header:   Authorization: Bearer your-secret-token
# Body:     {"jsonrpc":"2.0","id":1,"method":"tools/call","params":{...}}
```

## Environment variables

### Required (all transports)

| Variable | Description |
|---|---|
| `VAULTRUN_BASE_URL` | Base URL of the VaultRun API |
| `VAULTRUN_API_KEY` | VaultRun API key |

### Optional (all transports)

| Variable | Default | Description |
|---|---|---|
| `VAULTRUN_DEFAULT_IMAGE` | `python:3.12-slim` | Default Docker image for new sessions |
| `VAULTRUN_LOG_FILE` | stderr | Write server logs to this file |
| `GITHUB_TOKEN` | — | GitHub PAT — required for `run_github_repo` and `github_post_comment` |
| `MCP_FS_ALLOWED_PATHS` | — | Comma-separated absolute paths for filesystem tools. Filesystem tools are disabled when unset. |
| `MCP_AWS_ENABLED` | `false` | Set `true` to activate all AWS tools. Explicit opt-in prevents accidental activation in EC2/ECS with instance roles. |
| `AWS_REGION` | `us-east-1` | AWS region |
| `AWS_ACCESS_KEY_ID` | — | Static credentials (falls back to IAM role / env chain) |
| `AWS_SECRET_ACCESS_KEY` | — | Required when `AWS_ACCESS_KEY_ID` is set |
| `AWS_ENDPOINT_URL` | — | Custom endpoint for MinIO / LocalStack |
| `MCP_S3_FORCE_PATH_STYLE` | `false` | Path-style S3 addressing (needed for MinIO) |
| `MCP_SQLITE_PATH` | — | Path to a SQLite database file |
| `MCP_PG_DSN` | — | PostgreSQL DSN, e.g. `postgres://user:pass@host/db` |
| `MCP_MONGO_URI` | — | MongoDB URI, e.g. `mongodb://localhost:27017` |
| `MCP_MONGO_DB` | `test` | MongoDB database name |

### HTTP transport only (`MCP_TRANSPORT=http`)

| Variable | Default | Description |
|---|---|---|
| `MCP_AUTH_TOKEN` | — | Bearer token (required — server refuses to start without it) |
| `MCP_PORT` | `:8090` | Listen address |
| `MCP_ALLOWED_ORIGINS` | `*` | CORS allowed origins (comma-separated list for production) |
| `MCP_RATE_LIMIT` | `60` | Requests per minute per IP |
| `MCP_TRUSTED_PROXIES` | — | CIDRs of trusted reverse proxies |
| `MCP_TLS_CERT` | — | Path to TLS certificate (PEM) |
| `MCP_TLS_KEY` | — | Path to TLS private key (PEM) |
| `MCP_ACME_DOMAIN` | — | Domain for automatic Let's Encrypt TLS |
| `MCP_ACME_CACHE_DIR` | — | Directory for ACME challenge cache |
| `MCP_ACME_EMAIL` | — | Contact email for ACME registration |
| `MCP_FLOWD_ENABLED` | `false` | Set `true` to expose 6 `flowd_*` tools via local `flowctl` |
| `FLOWCTL_PATH` | `flowctl` | Path to flowctl binary |
| `FLOWD_CONFIG` | — | Optional config file for flowctl (`--config`) |

## All tools

Core: **53 tools**. With `MCP_FLOWD_ENABLED=true`: **59 tools**.

### Sandbox (13 tools)

| Tool | Required params | Description |
|---|---|---|
| `create_session` | — | Create an isolated Docker sandbox. Returns `session_id`. |
| `list_sessions` | — | List all active sessions. |
| `get_session` | `session_id` | Get session details (status, image, resource limits). |
| `delete_session` | `session_id` | Delete session, container, and workspace. |
| `run_command` | `session_id`, `command` | Execute a command inside the sandbox. |
| `upload_file` | `session_id`, `path`, `content` | Write a file into the sandbox workspace. |
| `read_file` | `session_id`, `path` | Read a file from the sandbox workspace. |
| `list_files` | `session_id` | List files in the sandbox workspace. |
| `delete_file` | `session_id`, `path` | Delete a file from the sandbox workspace. |
| `get_run` | `run_id` | Get run details (stdout, stderr, exit code, duration). |
| `list_runs` | `session_id` | List all command runs for a session. |
| `get_session_stats` | `session_id` | Get CPU and memory usage for a session. |
| `get_session_logs` | `session_id` | Get container logs (stdout + stderr stream). |

### Docker images (2 tools)

| Tool | Required params | Description |
|---|---|---|
| `list_images` | — | List available Docker images on the host. |
| `pull_image` | `image` | Pull a Docker image from the registry. |

### Snapshots (2 tools)

| Tool | Required params | Description |
|---|---|---|
| `create_snapshot` | `session_id`, `name` | Save the current workspace state as a named snapshot. |
| `list_snapshots` | `session_id` | List snapshots for a session. |

### Artifacts & audit (3 tools)

| Tool | Required params | Description |
|---|---|---|
| `create_artifact` | `session_id`, `file_path`, `name` | Publish a file as a shared artifact (S3 or local). |
| `list_artifacts` | — | List all shared artifacts. |
| `list_audit_logs` | — | Browse the HMAC-signed audit log. Supports `session_id`, `actor`, `action`, `limit`. |

### GitHub (2 tools)

Requires `GITHUB_TOKEN` env var.

| Tool | Required params | Description |
|---|---|---|
| `run_github_repo` | `owner`, `repo` | Clone a GitHub repo into a session and run a command. Uses `http.extraheader` — token never in URL. |
| `github_post_comment` | `owner`, `repo`, `number`, `body` | Post a comment on a GitHub issue or pull request. |

### Filesystem (4 tools)

Requires `MCP_FS_ALLOWED_PATHS` to be set. All paths are validated against the allowlist.

| Tool | Required params | Description |
|---|---|---|
| `fs_read_file` | `path` | Read a file from the server filesystem. |
| `fs_write_file` | `path`, `content` | Write a file to the server filesystem. |
| `fs_list_dir` | `path` | List directory contents on the server filesystem. |
| `fs_delete_file` | `path` | Delete a file from the server filesystem. |

### AWS — S3 (6 tools)

Requires `MCP_AWS_ENABLED=true`.

| Tool | Required params | Description |
|---|---|---|
| `s3_list_buckets` | — | List S3 buckets in the account. |
| `s3_list_objects` | `bucket` | List objects in a bucket. Optional: `prefix`, `max_keys`. |
| `s3_get_object` | `bucket`, `key` | Download an S3 object (max 10 MB, base64 for binary). |
| `s3_put_object` | `bucket`, `key`, `body` | Upload a string or base64-encoded object to S3. |
| `s3_delete_object` | `bucket`, `key` | Delete an S3 object. |
| `s3_head_object` | `bucket`, `key` | Get metadata for an S3 object without downloading. |

### AWS — SSM Parameter Store (4 tools)

Requires `MCP_AWS_ENABLED=true`.

| Tool | Required params | Description |
|---|---|---|
| `ssm_get_parameter` | `name` | Get a parameter. SecureString values require `with_decryption=true`. |
| `ssm_put_parameter` | `name`, `value` | Create or update a parameter. Optional: `type` (String/SecureString/StringList). |
| `ssm_delete_parameter` | `name` | Delete a parameter. |
| `ssm_list_parameters` | — | List parameters. Optional: `path` prefix, `max_results`. |

### AWS — Secrets Manager (2 tools)

Requires `MCP_AWS_ENABLED=true`. `sm_get_secret` is in the write rate-limit tier and its result is redacted from audit logs.

| Tool | Required params | Description |
|---|---|---|
| `sm_get_secret` | `secret_id` | Retrieve a secret value. |
| `sm_list_secrets` | — | List secret metadata (names only, no values). |

### AWS — Lambda (2 tools)

Requires `MCP_AWS_ENABLED=true`. `lambda_invoke` is in the heavy rate-limit tier (max 6 MB payload).

| Tool | Required params | Description |
|---|---|---|
| `lambda_list_functions` | — | List Lambda functions in the configured region. |
| `lambda_invoke` | `function_name` | Invoke a Lambda function. Optional: `payload` (JSON), `invocation_type`. |

### SQLite (3 tools)

Requires `MCP_SQLITE_PATH`.

| Tool | Required params | Description |
|---|---|---|
| `sqlite_query` | `query` | Run a read-only SQL query (SELECT, PRAGMA). Returns formatted rows. |
| `sqlite_execute` | `statement` | Execute a write statement (INSERT/UPDATE/DELETE/DDL). Returns rows affected. |
| `sqlite_schema` | — | Return DDL (CREATE TABLE) for all or a specific `table`. |

### PostgreSQL (3 tools)

Requires `MCP_PG_DSN`.

| Tool | Required params | Description |
|---|---|---|
| `pg_query` | `query` | Run a read-only SQL query. Returns formatted rows. |
| `pg_execute` | `statement` | Execute a write statement. Returns rows affected. |
| `pg_schema` | — | Return column definitions from `information_schema`. Optional: `table`, `schema` (default `public`). |

### MongoDB (7 tools)

Requires `MCP_MONGO_URI` and `MCP_MONGO_DB`.

| Tool | Required params | Description |
|---|---|---|
| `mongo_find` | `collection` | Find documents. Optional: `filter` (JSON), `limit` (1–1000, default 20). |
| `mongo_insert_one` | `collection`, `document` | Insert a single document (JSON). |
| `mongo_update` | `collection`, `update` | Update documents with an operator document (`$set`, etc.). Optional: `filter`, `many=true`. |
| `mongo_delete` | `collection` | Delete documents. Optional: `filter`, `many=true`. |
| `mongo_aggregate` | `collection`, `pipeline` | Run an aggregation pipeline (JSON array of stages). |
| `mongo_collections` | — | List all collection names in the database. |
| `mongo_generate_mongoose` | `collection` | Sample up to 50 documents and generate a Mongoose schema (JavaScript). |

### Flowd — local workflows (6 tools)

Requires `MCP_FLOWD_ENABLED=true` and `flowctl` on the same host as `vaultrun-mcp`. See [docs/flowd-integration.md](../../docs/flowd-integration.md).

| Tool | Required params | Description |
|---|---|---|
| `flowd_list_suggestions` | — | List pending workflow suggestions from flowd. |
| `flowd_explain_suggestion` | — | Explain suggestions. Optional: `suggestion_id`. |
| `flowd_approve_suggestion` | `suggestion_id` | Approve a suggestion by ID. |
| `flowd_list_patterns` | — | List detected workflow patterns. |
| `flowd_stats` | — | Local Flowd usage statistics. |
| `flowd_undo_run` | `run_id` | Undo a Flowd automation run. |

## Security model

- **No shell** — all commands run through Docker exec API, never through a shell.
- **Non-root containers** — all capabilities dropped, seccomp default profile applied.
- **Network off by default** — `network_enabled` must be explicitly set to `true`.
- **Audit log** — every tool call is logged with tool name, parameters, client IP, and duration. Sensitive results (`sm_get_secret`, `ssm_get_parameter`) are redacted.
- **Rate limiting** — read tools: 60 req/min, write tools: 30 req/min, heavy tools (container creation, Lambda, GitHub clone): 10 req/min.
- **AWS opt-in** — `MCP_AWS_ENABLED=true` prevents accidental IAM credential activation.
- **Flowd opt-in** — `MCP_FLOWD_ENABLED=true` runs local `flowctl`; no network API in Flowd itself.
- **Filesystem allowlist** — filesystem tools return an error when `MCP_FS_ALLOWED_PATHS` is not set; paths are validated against the allowlist with symlink resolution.
- **Token never in URL** — GitHub clones use `http.extraheader` (`Authorization: Bearer ...`) so the token never appears in git remote URLs or log output.

## HTTP transport endpoints

```
POST /mcp     JSON-RPC 2.0 request/response
GET  /sse     Server-Sent Events (future: streaming tool results)
GET  /        Server info JSON (name, version, transport, tool count)
GET  /healthz Health check — returns {"ok":true}
```

Security headers on every response: `X-Content-Type-Options`, `X-Frame-Options`, `X-XSS-Protection`, `Referrer-Policy`, `Cache-Control: no-store`.

## Development

```bash
# Run all tests
go test ./sdk/mcp/... -v

# Run only DB tests (in-memory SQLite, no external services needed)
go test ./sdk/mcp/... -run TestSQLite -v

# Smoke-test HTTP transport
MCP_TRANSPORT=http MCP_AUTH_TOKEN=test \
VAULTRUN_BASE_URL=http://localhost:8080 VAULTRUN_API_KEY=vr_... \
./vaultrun-mcp &

curl -s -X POST http://localhost:8090/mcp \
  -H 'Authorization: Bearer test' \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  | jq '.result.tools | length'
# → 53 (59 with MCP_FLOWD_ENABLED=true)

# Without token → 401
curl -s -X POST http://localhost:8090/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  | jq .
# → {"error":"unauthorized"}
```
