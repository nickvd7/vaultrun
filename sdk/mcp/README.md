# VaultRun MCP Server

A [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server that exposes VaultRun sandbox capabilities as tools for AI agents (Claude, etc.).

## What it does

Gives any MCP-compatible AI host the ability to:

| Tool | What it does |
|---|---|
| `create_session` | Spin up an isolated Docker sandbox |
| `run_command` | Execute code inside a session |
| `upload_file` | Put files into the workspace |
| `read_file` | Read files back out |
| `list_files` | See everything in the workspace |
| `list_sessions` | List active sessions |
| `get_session` | Check session status |
| `delete_session` | Clean up when done |

## Build

```bash
# From the repository root:
go build -o vaultrun-mcp ./sdk/mcp/
```

Or install directly:

```bash
go install github.com/nickvd7/vaultrun/sdk/mcp@latest
```

## Configuration

The server reads these environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `VAULTRUN_BASE_URL` | ✓ | — | Base URL of your VaultRun API |
| `VAULTRUN_API_KEY` | ✓ | — | API key for authentication |
| `VAULTRUN_DEFAULT_IMAGE` | — | `python:3.12-slim` | Default Docker image for new sessions |
| `VAULTRUN_LOG_FILE` | — | stderr | Write logs here instead of stderr |

## Usage with Claude Desktop

Add to `~/.config/claude/claude_desktop_config.json` (macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`):

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

## Usage with Claude Code

Add to your project's `.claude/settings.json`:

```json
{
  "mcpServers": {
    "vaultrun": {
      "type": "stdio",
      "command": "/path/to/vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_yourkeyhere"
      }
    }
  }
}
```

Or via the CLI:

```bash
claude mcp add vaultrun /path/to/vaultrun-mcp \
  -e VAULTRUN_BASE_URL=http://localhost:8080 \
  -e VAULTRUN_API_KEY=vr_yourkeyhere
```

## Example agent interaction

Once configured, Claude can:

```
User: Can you run this Python script and show me the output?

Claude: I'll create a sandbox session and run it for you.

[calls create_session] → session_id: abc-123
[calls upload_file path=/script.py content=...]
[calls run_command command=python args=["script.py"]]
→ status: completed, exit_code: 0
--- stdout ---
Hello from VaultRun!
[calls delete_session session_id=abc-123]

Done! The script ran successfully and printed: "Hello from VaultRun!"
```

## Security notes

- The MCP server uses the configured API key for all requests. Use a **non-master key** with minimal permissions.
- Sessions run as `nobody` inside isolated Docker containers with all Linux capabilities dropped.
- Network is disabled by default; use `network_enabled=true` only when needed.
- Each session has a configurable timeout (default 300 s) and memory limit.
- All operations are audit-logged by the VaultRun server.
- The server validates `VAULTRUN_BASE_URL` and `VAULTRUN_API_KEY` at startup; it exits immediately if either is missing.

## Development

```bash
# Run tests:
go test ./sdk/mcp/ -v

# Build and test locally:
go build -o /tmp/vaultrun-mcp ./sdk/mcp/
VAULTRUN_BASE_URL=http://localhost:8080 VAULTRUN_API_KEY=vr_test /tmp/vaultrun-mcp
```

The server speaks MCP over stdin/stdout (one JSON object per line). You can test it manually:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}' | /tmp/vaultrun-mcp
```
