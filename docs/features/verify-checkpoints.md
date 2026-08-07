# Verify checkpoints

Status: **shipped**. Post-run / post-step assertions for agent workflows and Missions.

## Idea

After `run_command` (or a mission step), assert outcomes before continuing:

- `exit_code_zero` — require success (or non-zero)
- `stdout_contains` — substring match on stdout
- `file_exists` — workspace path must exist

Results can be persisted in `run_verifications` for audit / mission replay.

## API

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/verify` | Evaluate a spec (optionally against a `run_id`) |
| `GET` | `/api/v1/sessions/:id/verifications` | List recent results |

### Request shape

```json
{
  "spec": {
    "exit_code_zero": true,
    "stdout_contains": "Successfully",
    "file_exists": "/workspace/out.txt"
  },
  "run_id": "…",
  "session_id": "…",
  "step_name": "install deps",
  "persist": true
}
```

When `run_id` is set, exit code and stdout are loaded from the run. `file_exists` needs a session (from the run or explicit `session_id`).

## MCP

Tool: `verify_checkpoint` — same checks via tools/call (string args; typed JSON also coerced).

## Code

- `internal/verify` — evaluate + store
- `cmd/api/handlers/verify.go`
- Migration `018_run_verifications`
- `sdk/mcp/verify.go`
