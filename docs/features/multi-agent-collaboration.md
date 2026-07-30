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

### Critical Security Measures

#### Message Authentication

Prevent agent impersonation:

```go
// Server-side message authentication
type AgentConnection struct {
    AgentID   string
    APIKey    string
    SessionID uuid.UUID
    OrgID     uuid.UUID
    ws        *websocket.Conn
}

func (s *CollaborationServer) handleSendMessage(conn *AgentConnection, req *SendMessageRequest) error {
    // CRITICAL: Never trust client-provided "from" field
    // Server ALWAYS sets sender from authenticated connection
    
    message := Message{
        ID:        uuid.New(),
        From:      conn.AgentID,  // Server sets this based on auth
        To:        req.To,
        Body:      req.Body,
        Timestamp: time.Now(),
        SessionID: conn.SessionID,
    }
    
    // Validate sender is part of session
    if !s.isAgentInSession(conn.AgentID, conn.SessionID) {
        return errors.New("agent not authorized for this session")
    }
    
    // Validate recipient exists (if not broadcast)
    if message.To != "all" {
        if !s.isAgentInSession(message.To, conn.SessionID) {
            return errors.New("recipient not in session")
        }
    }
    
    // Sign message for integrity
    message.Signature = s.signMessage(&message)
    
    // Store in database
    s.db.SaveMessage(&message)
    
    // Broadcast to session
    return s.broadcast(conn.SessionID, &message)
}

func (s *CollaborationServer) signMessage(msg *Message) string {
    data := fmt.Sprintf("%s:%s:%s:%s:%d",
        msg.ID, msg.From, msg.To, msg.Body, msg.Timestamp.Unix())
    
    h := hmac.New(sha256.New, s.signingKey)
    h.Write([]byte(data))
    return hex.EncodeToString(h.Sum(nil))
}
```

#### File Race Condition Protection

Implement optimistic locking for concurrent file edits:

```go
type FileVersion struct {
    Path           string
    Version        int
    ContentHash    string  // SHA-256 of content
    LastModifiedBy string
    LastModifiedAt time.Time
}

type FileSync struct {
    versions map[string]*FileVersion
    mu       sync.RWMutex
    db       *sql.DB
}

func (f *FileSync) WriteFile(agentID, sessionID uuid.UUID, path string, content []byte, expectedVersion int) error {
    f.mu.Lock()
    defer f.mu.Unlock()
    
    key := fmt.Sprintf("%s:%s", sessionID, path)
    current := f.versions[key]
    
    // Check version
    if current != nil && current.Version != expectedVersion {
        return &ConflictError{
            Message: "File modified by another agent",
            CurrentVersion: current.Version,
            ExpectedVersion: expectedVersion,
            LastModifiedBy: current.LastModifiedBy,
            LastModifiedAt: current.LastModifiedAt,
        }
    }
    
    // Atomic write using temp file
    tmpPath := path + ".tmp." + uuid.New().String()
    if err := ioutil.WriteFile(tmpPath, content, 0644); err != nil {
        return err
    }
    
    // Atomic rename
    if err := os.Rename(tmpPath, path); err != nil {
        os.Remove(tmpPath)
        return err
    }
    
    // Update version
    newVersion := &FileVersion{
        Path:           path,
        Version:        current.Version + 1,
        ContentHash:    sha256Sum(content),
        LastModifiedBy: agentID.String(),
        LastModifiedAt: time.Now(),
    }
    
    f.versions[key] = newVersion
    
    // Store in database
    f.db.SaveFileVersion(sessionID, newVersion)
    
    // Broadcast change
    f.broadcast(sessionID, FileChangedEvent{
        Path:       path,
        Version:    newVersion.Version,
        ModifiedBy: agentID,
        ContentHash: newVersion.ContentHash,
    })
    
    return nil
}

// Agent retry logic with exponential backoff
func (a *Agent) writeFileWithRetry(path string, content []byte) error {
    maxRetries := 3
    backoff := 100 * time.Millisecond
    
    for i := 0; i < maxRetries; i++ {
        // Get current version
        version := a.getFileVersion(path)
        
        // Attempt write
        err := a.client.WriteFile(a.sessionID, path, content, version)
        if err == nil {
            return nil
        }
        
        // Check if conflict
        if conflictErr, ok := err.(*ConflictError); ok {
            log.Info("File conflict detected, retrying",
                "path", path,
                "attempt", i+1,
                "modified_by", conflictErr.LastModifiedBy,
            )
            
            // Exponential backoff
            time.Sleep(backoff * time.Duration(1<<i))
            
            // Re-read file and merge changes if possible
            continue
        }
        
        // Other error
        return err
    }
    
    return errors.New("max retries exceeded for file write")
}
```

#### Connection Rate Limiting

Prevent WebSocket storms:

```go
type ConnectionLimiter struct {
    attempts map[string][]time.Time  // agentID -> connection timestamps
    mu       sync.RWMutex
}

func (l *ConnectionLimiter) AllowConnection(agentID string) error {
    l.mu.Lock()
    defer l.mu.Unlock()
    
    now := time.Now()
    
    // Clean old attempts
    if attempts, exists := l.attempts[agentID]; exists {
        recent := []time.Time{}
        for _, t := range attempts {
            if now.Sub(t) < 1*time.Minute {
                recent = append(recent, t)
            }
        }
        l.attempts[agentID] = recent
    } else {
        l.attempts[agentID] = []time.Time{}
    }
    
    // Check limit
    if len(l.attempts[agentID]) >= 10 {
        return errors.New("connection rate limit exceeded (max 10/minute)")
    }
    
    // Record attempt
    l.attempts[agentID] = append(l.attempts[agentID], now)
    return nil
}

func (s *CollaborationServer) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
    agentID := r.Header.Get("X-Agent-ID")
    apiKey := r.Header.Get("X-API-Key")
    
    // Rate limit check
    if err := s.connLimiter.AllowConnection(agentID); err != nil {
        http.Error(w, "Too many connection attempts", http.StatusTooManyRequests)
        return
    }
    
    // Authenticate
    if !s.auth.ValidateAPIKey(apiKey) {
        http.Error(w, "Invalid API key", http.StatusUnauthorized)
        return
    }
    
    // Upgrade to WebSocket
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        return
    }
    
    // Handle connection
    s.handleAgentConnection(agentID, apiKey, conn)
}
```

#### Agent Disconnect Recovery

Handle partial file writes on disconnect:

```go
type FileTransaction struct {
    ID        uuid.UUID
    AgentID   string
    Path      string
    StartedAt time.Time
    Status    string  // "in_progress", "committed", "aborted"
}

func (f *FileSync) BeginTransaction(agentID, path string) (*FileTransaction, error) {
    tx := &FileTransaction{
        ID:        uuid.New(),
        AgentID:   agentID,
        Path:      path,
        StartedAt: time.Now(),
        Status:    "in_progress",
    }
    
    f.db.SaveTransaction(tx)
    return tx, nil
}

func (f *FileSync) CommitTransaction(txID uuid.UUID, content []byte) error {
    tx := f.db.GetTransaction(txID)
    if tx == nil {
        return errors.New("transaction not found")
    }
    
    // Atomic write
    if err := f.writeFileAtomic(tx.Path, content); err != nil {
        tx.Status = "aborted"
        f.db.UpdateTransaction(tx)
        return err
    }
    
    tx.Status = "committed"
    f.db.UpdateTransaction(tx)
    return nil
}

// Cleanup orphaned transactions
func (f *FileSync) CleanupOrphanedTransactions() {
    orphans := f.db.GetTransactions(TransactionQuery{
        Status:    "in_progress",
        OlderThan: time.Now().Add(-5 * time.Minute),
    })
    
    for _, tx := range orphans {
        log.Warn("Cleaning up orphaned transaction",
            "tx_id", tx.ID,
            "agent_id", tx.AgentID,
            "path", tx.Path,
        )
        
        tx.Status = "aborted"
        f.db.UpdateTransaction(tx)
    }
}
```

### Additional Security

### Agent Authentication

- Each agent needs valid API key
- Agents can only join sessions in their org
- Session owner can kick agents out
- All WebSocket connections are authenticated

### Rate Limiting

- Max 10 connections per minute per agent
- Max 100 messages per minute per agent
- Max 1000 file changes per minute per session

### Privacy

- Agent messages are audited
- Full message history stored for compliance
- Admins can view all agent communication
- All messages are signed with HMAC

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
