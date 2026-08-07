# Missions — reusable verified tool sequences

Status: **foundation + cost attribution shipped** (API + storage). Inspired by workflow-as-asset patterns (save the plan, replay later).

## Idea

A **mission** is a named, versioned sequence of MCP/API tool steps (e.g. `run_command` + verify). Successful agent work becomes an owned asset: inspect steps, record runs against a session, and (later) replay with cost attribution.

## API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/missions` | List (published by default for non-master) |
| `POST` | `/api/v1/missions` | Create |
| `GET` | `/api/v1/missions/:id` | Get |
| `GET` | `/api/v1/missions/slug/:slug` | Get by slug |
| `PUT` | `/api/v1/missions/:id` | Update |
| `DELETE` | `/api/v1/missions/:id` | Delete |
| `POST` | `/api/v1/missions/:id/runs` | Record a run (optional `session_id`) |
| `GET` | `/api/v1/missions/:id/runs` | List recent runs |
| `GET` | `/api/v1/missions/:id/runs/:run_id` | Get one run |
| `PATCH` | `/api/v1/missions/:id/runs/:run_id` | Update status / step_results (`attribute_costs` optional) |
| `POST` | `/api/v1/missions/:id/runs/:run_id/attribute-costs` | Snapshot session cost_metrics onto the run |
| `GET` | `/api/v1/missions/:id/runs/:run_id/costs` | Read cost attribution for a run |
| `GET` | `/api/v1/missions/:id/costs` | Aggregate attributed costs for the mission |

## Cost attribution (replay)

`cost_metrics` rows stay immutable. When a run has a `session_id`, call **attribute-costs** (or `PATCH` with `"attribute_costs": true`) to snapshot compute/storage/network totals into `mission_cost_attributions`. That links mission replay history to spend without mutating billing rows.

## Step shape

```json
{
  "name": "install deps",
  "tool": "run_command",
  "args": { "session_id": "{{session}}", "command": "pip", "args": "[\"install\",\"-r\",\"requirements.txt\"]" },
  "verify": { "exit_code_zero": true, "stdout_contains": "Successfully" }
}
```

`verify` is stored for later evaluation by the run-verify feature; this foundation records structure and run metadata only.

## Code

- `internal/missions`
- `cmd/api/handlers/missions.go`
- Migration `017_missions`
