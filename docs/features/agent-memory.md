# Agent memory (sandbox)

Status: **shipped**. Persistent key/value notes inside a session workspace.

## Idea

Agents often need durable scratch notes across tool calls (preferences, discovered paths, prior decisions). VaultRun stores them as files under:

```
.vaultrun/memory/<key>
```

They live in the sandbox workspace, so they survive for the session lifetime and are included in workspace **snapshots**.

## MCP tools

| Tool | Purpose |
|------|---------|
| `memory_set` | Write `key` → `value` |
| `memory_get` | Read `key` |
| `memory_list` | List keys |
| `memory_delete` | Delete `key` |

Keys: letters, digits, `.` `_` `/` `-` (max 200); no `..` or absolute paths.

## Code

- `sdk/mcp/memory.go`
