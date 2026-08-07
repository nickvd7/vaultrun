# Agent swarm graph

Status: **foundation shipped**. Directed topology edges between agents in a collaborative session.

## Idea

Multi-agent sessions already have presence + messaging. The **swarm graph** records how agents relate (lead/reviewer/handoff/peer) so dashboards and orchestrators can reason about topology.

## Relations

| Relation | Meaning |
|----------|---------|
| `reports_to` | Worker → lead |
| `reviews` | Reviewer → author |
| `handoff` | Transfer of ownership |
| `peer` | Symmetric collaborators (still stored directed) |

## API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/sessions/:id/graph` | Agents + edges |
| `POST` | `/api/v1/sessions/:id/graph/edges` | Upsert edge |
| `DELETE` | `/api/v1/sessions/:id/graph/edges/:edge_id` | Remove edge |
| `GET` | `/api/v1/sessions/:id/agents?include_graph=true` | Agents + edges |

Requires collaboration enabled (`POST …/enable-collaboration`) and Redis for live presence; edges are Postgres-backed.

## Code

- `internal/collab/graph.go`
- Migration `019_agent_graph_edges`
- Handlers on `CollabHandler`
