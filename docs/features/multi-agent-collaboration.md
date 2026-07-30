# Live Multi-Agent Collaboration

## Executive Summary

Meerdere AI agents kunnen tegelijkertijd werken in dezelfde VaultRun sandbox met real-time synchronisatie, conflict resolution, en agent-to-agent messaging. Dit maakt "agent swarms" mogelijk waarbij agents samenwerken aan complexe taken.

**Status:** 📈 Prioriteit 2  
**Effort:** Large (4-6 weken)  
**Dependencies:** WebSocket infrastructure, CRDT voor file sync

---

## Problem Statement

Huidige AI agent platforms isoleren agents volledig. Dit voorkomt:
- **Specialisatie** — Een agent voor backend, één voor frontend, één voor tests
- **Parallel work** — Meerdere agents werken tegelijkertijd aan verschillende files
- **Peer review** — Agent A schrijft code, Agent B reviewt
- **Knowledge sharing** — Agents delen geleerde context met elkaar

**Use Cases:**
- Software team van 3 agents: architect, developer, tester
- Data pipeline: één agent scraped, één transformeert, één analyseert
- Code review workflow: één agent schrijft, één reviewt, één fix issues

---

## Solution Overview

### Core Features

1. **Shared workspace** — Meerdere agents verbonden met zelfde session
2. **Real-time file sync** — CRDT-based merge van concurrent edits
3. **Agent messaging** — Pub/sub messaging binnen session
4. **Presence awareness** — Zie welke agents actief zijn en waar ze aan werken
5. **Conflict resolution** — Automatic merge of user-mediated conflict handling

### Architecture

```
Session sess_abc123
├── Agent A (architect)  ──┐
├── Agent B (developer)  ──┼── WebSocket Server ── Session State
├── Agent C (tester)     ──┘         ↓
└── Files (CRDT)              Redis Pub/Sub
```

---

## Technical Design

### 1. Connection Model

**WebSocket per agent:**

```
wss://vaultrun.dev/ws/sessions/sess_abc123?api_key=vr_...&agent_id=agent_a
```

**Session state in Redis:**

```redis
# Active agents
SET session:sess_abc123:agents:agent_a {"name": "Architect", "connected_at": "..."}
SET session:sess_abc123:agents:agent_b {"name": "Developer", "connected_at": "..."}

# File locks (optional)
SET session:sess_abc123:locks:main.py "agent_b" EX 60

# Message queue
LPUSH session:sess_abc123:messages {"from": "agent_a", "to": "agent_b", "body": "..."}
```

### 2. File Synchronization

**Option A: Operational Transform (Complex)**
- Gebruikt bij Google Docs, VSCode Live Share
- Garanteert convergence
- Complex te implementeren

**Option B: Last-Write-Wins met Timestamps (Simpel)**
- File heeft `last_modified_by` + `version`
- Conflicten = error, agents moeten retry
- Goed genoeg voor MVP (agents werken meestal aan verschillende files)

**Option C: Yjs CRDT (Recommended)**
- [Yjs](https://github.com/yjs/yjs) — battle-tested CRDT library
- Automatic merge van concurrent edits
- WebSocket sync provider beschikbaar
- Good balance tussen complexity en robustness

**Recommendation:** Start met Option B (simpel), upgrade naar Option C als agents vaak conflicteren.

### 3. Messaging Protocol

**Agent-to-agent messages:**

```json
{
  "type": "message",
  "from": "agent_a",
  "to": "agent_b",
  "body": "I've updated the API schema in schema.yaml. Please update the client code accordingly.",
  "timestamp": "2026-07-30T17:10:00Z"
}
```

**Broadcast messages:**

```json
{
  "type": "broadcast",
  "from": "agent_a",
  "body": "Running tests now, please don't modify test files.",
  "timestamp": "2026-07-30T17:10:05Z"
}
```

**System events:**

```json
{
  "type": "agent_joined",
  "agent_id": "agent_c",
  "agent_name": "Tester",
  "timestamp": "2026-07-30T17:10:10Z"
}

{
  "type": "file_changed",
  "file": "main.py",
  "changed_by": "agent_b",
  "timestamp": "2026-07-30T17:10:15Z"
}
```

### 4. Presence & Awareness

**Tracking:**
- Which agents are connected
- What file each agent is working on
- Last activity timestamp
- Cursor position (advanced)

**API:**

```bash
GET /api/v1/sessions/sess_abc123/agents

Response:
{
  "agents": [
    {
      "id": "agent_a",
      "name": "Architect",
      "status": "active",
      "current_file": "schema.yaml",
      "last_activity": "2026-07-30T17:10:00Z"
    },
    {
      "id": "agent_b",
      "name": "Developer",
      "status": "idle",
      "current_file": null,
      "last_activity": "2026-07-30T17:08:30Z"
    }
  ]
}
```

---

## Implementation Plan

### Phase 1: WebSocket Infrastructure (Week 1-2)

- [ ] Add WebSocket server to API (`cmd/api/ws/`)
- [ ] Agent connection handling (auth via API key)
- [ ] Redis pub/sub for session events
- [ ] Presence tracking (join/leave events)

### Phase 2: File Sync (Week 2-3)

- [ ] Implement last-write-wins with version numbers
- [ ] File change notifications over WebSocket
- [ ] Conflict detection + error handling
- [ ] Integration tests for concurrent writes

### Phase 3: Messaging (Week 3-4)

- [ ] Agent-to-agent messaging
- [ ] Broadcast messaging
- [ ] Message history (last 100 messages per session)
- [ ] MCP tools: `agent_send_message`, `agent_get_messages`

### Phase 4: Advanced Features (Week 4-6)

- [ ] Upgrade to Yjs CRDT (if needed)
- [ ] File locking mechanism (optional)
- [ ] Dashboard UI for multi-agent sessions
- [ ] Agent activity timeline visualization

---

## API & MCP Tools

### New MCP Tools

```go
{
    Name:        "agent_list_peers",
    Description: "List all agents connected to this session",
}

{
    Name:        "agent_send_message",
    Description: "Send a message to another agent or broadcast to all",
    InputSchema: {
        "to": "agent_id or 'all'",
        "body": "message text"
    },
}

{
    Name:        "agent_get_messages",
    Description: "Get recent messages in this session",
    InputSchema: {
        "since": "timestamp (optional)",
        "limit": "max messages to return"
    },
}

{
    Name:        "agent_set_status",
    Description: "Update your agent's status and current file",
    InputSchema: {
        "status": "active|idle|busy",
        "current_file": "file path (optional)"
    },
}
```

---

## Configuration

```bash
# Enable multi-agent features
MULTI_AGENT_ENABLED=true

# Max agents per session
MULTI_AGENT_MAX_PER_SESSION=10

# WebSocket settings
WS_PORT=8081
WS_HEARTBEAT_INTERVAL=30s

# Redis for pub/sub
REDIS_URL=redis://localhost:6379
```

---

## Security Considerations

### Agent Authentication

- Each agent needs valid API key
- Agents can only join sessions in their org
- Session owner can kick agents out

### Rate Limiting

- Max 100 messages per minute per agent
- Max 1000 file changes per minute per session

### Privacy

- Agent messages are audited
- Full message history stored for compliance
- Admins can view all agent communication

---

## Example Use Cases

### Use Case 1: Software Development Team

```python
# Agent A (Architect) creates session
session = create_session(name="microservice-refactor")

# Agent B (Developer) joins
agent_send_message(session_id, to="all", body="Starting implementation of user service")

# Agent C (Tester) joins
agent_send_message(session_id, to="agent_b", body="Can you add unit tests for the new endpoints?")

# Agent B responds
agent_send_message(session_id, to="agent_c", body="Sure, I'll add them after implementing the handlers")

# Parallel work
# Agent B: upload_file(session_id, "user_service.py", code)
# Agent C: upload_file(session_id, "test_user_service.py", tests)

# Agent A reviews
peers = agent_list_peers(session_id)
# See that both Agent B and C are active
```

### Use Case 2: Data Pipeline

```python
# Agent A: Data scraper
run_command(session_id, "python", ["scrape.py"])
agent_send_message(to="agent_b", body="Raw data ready in raw_data.json")

# Agent B: Data transformer (starts after message)
run_command(session_id, "python", ["transform.py"])
agent_send_message(to="agent_c", body="Cleaned data ready in clean_data.csv")

# Agent C: Analyst (starts after message)
run_command(session_id, "python", ["analyze.py"])
```

---

## Dashboard UI

### Session Detail Page Updates

**New "Agents" tab:**

```
┌─────────────────────────────────────────────────┐
│ Active Agents (3)                               │
├─────────────────────────────────────────────────┤
│ ● Architect        │ schema.yaml  │ 2 min ago   │
│ ● Developer        │ main.py      │ Just now    │
│ ○ Tester (idle)    │ —            │ 10 min ago  │
└─────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────┐
│ Recent Messages                                 │
├─────────────────────────────────────────────────┤
│ [17:10] Architect → Developer:                  │
│   "I've updated the schema, please sync"        │
│                                                 │
│ [17:11] Developer → All:                        │
│   "Running tests now"                           │
└─────────────────────────────────────────────────┘
```

---

## Success Metrics

- % of sessions with multiple agents (target: 15% after 6 months)
- Avg agents per multi-agent session (target: 2.5)
- Messages sent per multi-agent session (indicator of collaboration)
- User feedback on collaboration workflows

---

## Future Enhancements

- **Video/audio for human-agent collaboration** — WebRTC
- **Shared terminal** — Multiple agents/humans see same terminal output
- **File locking with auto-release** — Lock expires after 5 min of inactivity
- **Agent roles** — Lead, contributor, viewer
- **Replay multi-agent sessions** — See timeline of who did what

---

## References

- WebSocket: `github.com/gorilla/websocket`
- CRDT: `github.com/yjs/yjs` (via Wasm or port to Go)
- Redis pub/sub: existing Redis integration
- VSCode Live Share architecture: https://aka.ms/vsls-arch
