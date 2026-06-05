# MCP Server — Deep-Dive Guide

This guide covers everything needed to run the VaultRun MCP server in production:
transport selection, TLS, authentication, rate limiting, and per-tool examples.

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
| `POST` | `/mcp` | JSON-RPC 2.0 request/response |
| `GET` | `/sse` | Server-Sent Events (future: streaming results) |
| `GET` | `/` | Server info JSON |
| `GET` | `/healthz` | Health check — returns `{"ok":true}` |

### Authentication

Every `POST /mcp` request must include:

```
Authorization: Bearer your-secret-token
```

Missing or wrong token → `401 Unauthorized`.

### Test with curl

```bash
# tools/list
curl -s -X POST http://localhost:8090/mcp \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  | jq '.result.tools | length'
# → 53

# tools/call
curl -s -X POST http://localhost:8090/mcp \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0","id":2,"method":"tools/call",
    "params":{"name":"list_sessions","arguments":{}}
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

## Security checklist for production

- [ ] `MCP_AUTH_TOKEN` is a long random value (32+ chars): `openssl rand -hex 32`
- [ ] TLS enabled (Let's Encrypt or static cert or reverse proxy)
- [ ] `MCP_ALLOWED_ORIGINS` set to specific origin(s), not `*`
- [ ] `MCP_TRUSTED_PROXIES` set when running behind a reverse proxy
- [ ] `MCP_AWS_ENABLED` only set if AWS tools are needed
- [ ] `MCP_FS_ALLOWED_PATHS` scoped to the minimum required directories
- [ ] VaultRun API key is a scoped key (not the master key): `make bootstrap-key`
- [ ] `GITHUB_TOKEN` has minimum scopes (`repo` read or `public_repo` for public repos only)
- [ ] Rate limits tuned for expected traffic (`MCP_RATE_LIMIT`)
- [ ] Log file configured and rotated (`VAULTRUN_LOG_FILE`)
