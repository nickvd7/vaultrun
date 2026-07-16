# VaultRun + Flowd integration

VaultRun runs AI agents in isolated Docker sandboxes. [Flowd](https://flowd.net) observes local file workflows, detects patterns, and suggests automations you approve from the terminal. This guide covers the **MCP bridge** that exposes Flowd to agents through `vaultrun-mcp`.

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────┐
│  AI agent       │────▶│  vaultrun-mcp    │────▶│  VaultRun   │
│  (Claude, etc.) │     │  flowd_* tools   │     │  API        │
└─────────────────┘     └────────┬─────────┘     └─────────────┘
                                 │ subprocess
                                 ▼
                        ┌──────────────────┐
                        │  flowctl /       │
                        │  flow-daemon     │
                        │  (local SQLite)  │
                        └──────────────────┘
```

Flowd has no HTTP API. The bridge runs `flowctl` on the same host as `vaultrun-mcp`.

## Prerequisites

1. **VaultRun** running (`make up`, API key via `make bootstrap-key`)
2. **Flowd** installed from source:

   ```bash
   git clone https://github.com/nickvd7/flowd && cd flowd
   cargo install --path crates/flow-cli
   cargo install --path crates/flow-daemon
   flowctl setup --watch ~/Downloads
   flow-daemon
   ```

3. **vaultrun-mcp** built: `go build -o vaultrun-mcp ./sdk/mcp/`

## Enable Flowd tools

Set `MCP_FLOWD_ENABLED=true` (explicit opt-in, same pattern as AWS tools):

```bash
MCP_FLOWD_ENABLED=true \
FLOWCTL_PATH=flowctl \
VAULTRUN_BASE_URL=http://localhost:8080 \
VAULTRUN_API_KEY=vr_your_key \
./vaultrun-mcp
```

Optional:

| Variable | Default | Description |
|----------|---------|-------------|
| `FLOWCTL_PATH` | `flowctl` | Path to the flowctl binary |
| `FLOWD_CONFIG` | — | Config file passed as `--config` to every flowctl invocation |

### Claude Desktop

```json
{
  "mcpServers": {
    "vaultrun": {
      "command": "/path/to/vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_your_key",
        "MCP_FLOWD_ENABLED": "true",
        "FLOWCTL_PATH": "flowctl"
      }
    }
  }
}
```

## MCP tools (6)

| Tool | flowctl | Description |
|------|---------|-------------|
| `flowd_list_suggestions` | `suggestions` | List pending automation suggestions |
| `flowd_explain_suggestion` | `suggestions --explain` | Show reasoning; optional `suggestion_id` |
| `flowd_approve_suggestion` | `approve <id>` | Approve a suggestion (`suggestion_id` required) |
| `flowd_list_patterns` | `patterns` | List detected workflow patterns |
| `flowd_stats` | `stats` | Local usage statistics |
| `flowd_undo_run` | `undo <run_id>` | Undo a previous run (`run_id` required) |

With Flowd enabled, `tools/list` returns **59** tools (53 core + 6 Flowd).

## Example agent workflow

1. Agent calls `flowd_list_suggestions` — user sees pending automations.
2. Agent calls `flowd_explain_suggestion` with `suggestion_id` — inspect the why.
3. User approves → agent calls `flowd_approve_suggestion`.
4. For scripts or untrusted code → `create_session` + `run_command` in VaultRun (audited sandbox).

## Limitations (v1)

- **Same host** — `flowctl` must run where `vaultrun-mcp` runs.
- **No `run` / `dry-run` bridge** — approved automations still execute locally via flowctl, not inside VaultRun containers.
- **No Flowd repo changes** — this integration lives entirely in VaultRun.
- **30s timeout** per flowctl invocation.

## Troubleshooting

| Error | Fix |
|-------|-----|
| `Flowd is not enabled` | Set `MCP_FLOWD_ENABLED=true` |
| `flowctl not found` | Install Flowd CLI; set `FLOWCTL_PATH` |
| Empty suggestions | Ensure `flow-daemon` is running and watching folders |
| Timeout | Check flowctl manually: `flowctl suggestions` |

## Links

- [Flowd](https://flowd.net) · [Flowd source](https://github.com/nickvd7/flowd)
- [Companion page](https://vaultrun.dev/flowd.html)
- [MCP server README](../sdk/mcp/README.md)
