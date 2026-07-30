# Session Replay & Time-Travel Debugging

## Executive Summary

Session Replay maakt het mogelijk om elke command execution binnen een VaultRun sandbox te herstellen naar een eerder moment. Dit lost een fundamenteel probleem op: **agent debugging is moeilijk**. Met replay kunnen developers en agents exact zien wat er gebeurde, waarom iets fout ging, en alternatieve paden testen vanaf elk checkpoint.

**Status:** 🎯 Prioriteit 1  
**Effort:** Medium (2-3 weken implementatie)  
**Dependencies:** Bestaande snapshot infrastructuur (`internal/snapshot/`)

---

## Problem Statement

### Current Pain Points

1. **Agent debugging is een black box** — Als een agent faalt na 20 commands, moet je opnieuw beginnen en hopen dat je de bug reproduceert
2. **Geen "undo" voor destructieve operaties** — `rm -rf` in een sandbox = session gone
3. **Moeilijk om alternatieve paden te testen** — "Wat als ik dit commando anders had uitgevoerd?"
4. **Audit trail toont wat, niet waarom** — Je ziet commands, maar niet de sandbox state op elk moment

### Impact

- **Developers** verspillen tijd aan reproduceren van bugs
- **AI agents** kunnen niet leren van hun fouten zonder volledige herhalingen
- **Kostenverspilling** door herbouwde sessions na failures

---

## Solution Overview

### Core Concept

Bij elke command execution:
1. **Pre-execution snapshot** — Capture workspace state vóór het commando
2. **Command execution** — Run normaal via Docker exec
3. **Post-execution snapshot** — Capture workspace state + output
4. **Checkpoint creation** — Maak een "replay point" met metadata

Users kunnen:
- **List checkpoints** — Zie alle momenten in de sessie history
- **Restore checkpoint** — Herstel sandbox naar exact die state
- **Fork checkpoint** — Maak nieuwe session vanaf een checkpoint
- **Replay command** — Re-run een commando vanaf een checkpoint

### Visual Flow

```
Session Start
    │
    ├─ Checkpoint 0: Initial state
    │
    ├─ run_command("pip install requests")
    │  └─ Checkpoint 1: packages installed (2.4s, exit 0)
    │
    ├─ run_command("python scrape.py")
    │  └─ Checkpoint 2: script executed (1.2s, exit 0)
    │
    ├─ run_command("rm data.json")  ← Oops, wrong file!
    │  └─ Checkpoint 3: file deleted (0.1s, exit 0)
    │
    └─ replay_restore(checkpoint_id=2)
       └─ Session state = "script executed", data.json exists again
```

---

## Architecture

### Data Model

#### New Table: `replay_checkpoints`

```sql
CREATE TABLE replay_checkpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id UUID REFERENCES runs(id) ON DELETE SET NULL,
    
    -- Checkpoint metadata
    checkpoint_number INT NOT NULL,  -- 0, 1, 2, ... within session
    name VARCHAR(255),                -- Optional user label
    description TEXT,                 -- Auto-generated or user-provided
    
    -- Snapshot references
    workspace_snapshot_id UUID NOT NULL REFERENCES snapshots(id),
    env_vars_snapshot JSONB,         -- Environment variables at this point
    
    -- Execution context
    command TEXT,                     -- The command that was run
    args JSONB,                       -- Command arguments array
    exit_code INT,
    duration_ms INT,
    stdout_preview TEXT,              -- First 500 chars
    stderr_preview TEXT,
    
    -- Metadata
    created_at TIMESTAMPTZ DEFAULT NOW(),
    size_bytes BIGINT,                -- Workspace snapshot size
    
    UNIQUE(session_id, checkpoint_number)
);

CREATE INDEX idx_replay_checkpoints_session ON replay_checkpoints(session_id, checkpoint_number DESC);
CREATE INDEX idx_replay_checkpoints_created ON replay_checkpoints(created_at DESC);
```

#### Updated Table: `runs`

```sql
ALTER TABLE runs ADD COLUMN checkpoint_id UUID REFERENCES replay_checkpoints(id);
ALTER TABLE runs ADD COLUMN restored_from_checkpoint_id UUID REFERENCES replay_checkpoints(id);
```

### Components

#### 1. `internal/replay/` (new package)

```go
package replay

type Checkpoint struct {
    ID              uuid.UUID
    SessionID       uuid.UUID
    RunID           *uuid.UUID
    Number          int
    Name            *string
    Description     string
    SnapshotID      uuid.UUID
    EnvVars         map[string]string
    Command         string
    Args            []string
    ExitCode        *int
    DurationMs      *int
    StdoutPreview   string
    StderrPreview   string
    CreatedAt       time.Time
    SizeBytes       int64
}

type Manager interface {
    // Create checkpoint after command execution
    CreateCheckpoint(ctx context.Context, opts CreateCheckpointOpts) (*Checkpoint, error)
    
    // List checkpoints for a session
    ListCheckpoints(ctx context.Context, sessionID uuid.UUID) ([]*Checkpoint, error)
    
    // Get specific checkpoint
    GetCheckpoint(ctx context.Context, id uuid.UUID) (*Checkpoint, error)
    
    // Restore session to checkpoint state
    RestoreCheckpoint(ctx context.Context, sessionID uuid.UUID, checkpointID uuid.UUID) error
    
    // Fork new session from checkpoint
    ForkFromCheckpoint(ctx context.Context, checkpointID uuid.UUID, newSessionName string) (*Session, error)
    
    // Delete old checkpoints (retention policy)
    PruneCheckpoints(ctx context.Context, sessionID uuid.UUID, keepLast int) error
}

type CreateCheckpointOpts struct {
    SessionID   uuid.UUID
    RunID       *uuid.UUID
    Name        *string
    Description string
    Command     string
    Args        []string
    ExitCode    *int
    DurationMs  *int
    Stdout      string
    Stderr      string
}
```

#### 2. `internal/runner/runner.go` (modifications)

Integreer checkpoint creation in bestaande run flow:

```go
func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Run, error) {
    // Existing pre-run logic...
    
    // NEW: Create pre-execution checkpoint (if enabled)
    var preCheckpoint *replay.Checkpoint
    if opts.EnableReplay {
        preCheckpoint, err = r.replayMgr.CreateCheckpoint(ctx, replay.CreateCheckpointOpts{
            SessionID:   opts.SessionID,
            Description: fmt.Sprintf("Before: %s %v", opts.Command, opts.Args),
        })
        if err != nil {
            // Log but don't fail the run
            log.Warn("Failed to create pre-checkpoint", "error", err)
        }
    }
    
    // Execute command (existing logic)
    result, err := r.docker.Exec(ctx, containerID, opts)
    
    // NEW: Create post-execution checkpoint
    if opts.EnableReplay && err == nil {
        postCheckpoint, err := r.replayMgr.CreateCheckpoint(ctx, replay.CreateCheckpointOpts{
            SessionID:   opts.SessionID,
            RunID:       &result.ID,
            Command:     opts.Command,
            Args:        opts.Args,
            ExitCode:    &result.ExitCode,
            DurationMs:  &result.DurationMs,
            Stdout:      result.Stdout,
            Stderr:      result.Stderr,
            Description: autoGenerateDescription(opts, result),
        })
        if err != nil {
            log.Warn("Failed to create post-checkpoint", "error", err)
        } else {
            result.CheckpointID = &postCheckpoint.ID
        }
    }
    
    return result, nil
}

func autoGenerateDescription(opts RunOptions, result *Run) string {
    status := "success"
    if result.ExitCode != 0 {
        status = fmt.Sprintf("failed (exit %d)", result.ExitCode)
    }
    return fmt.Sprintf("%s %v — %s in %dms", 
        opts.Command, opts.Args, status, result.DurationMs)
}
```

#### 3. REST API Endpoints (new)

**`cmd/api/handlers/replay.go`:**

```go
// GET /api/v1/sessions/:id/checkpoints
// List all checkpoints for a session
func ListCheckpoints(c *gin.Context)

// GET /api/v1/checkpoints/:id
// Get checkpoint details
func GetCheckpoint(c *gin.Context)

// POST /api/v1/sessions/:id/restore
// Body: {"checkpoint_id": "uuid"}
// Restore session to checkpoint state
func RestoreCheckpoint(c *gin.Context)

// POST /api/v1/checkpoints/:id/fork
// Body: {"name": "forked-session"}
// Create new session from checkpoint
func ForkCheckpoint(c *gin.Context)

// DELETE /api/v1/checkpoints/:id
// Delete specific checkpoint
func DeleteCheckpoint(c *gin.Context)

// POST /api/v1/sessions/:id/checkpoints/prune
// Body: {"keep_last": 10}
// Delete old checkpoints, keep N most recent
func PruneCheckpoints(c *gin.Context)
```

#### 4. MCP Tools (new)

**`sdk/mcp/tools.go`:**

```go
{
    Name:        "replay_list_checkpoints",
    Description: "List all replay checkpoints for a session. Shows command history with restore points.",
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "session_id": map[string]interface{}{
                "type":        "string",
                "description": "Session ID to list checkpoints for",
            },
        },
        "required": []string{"session_id"},
    },
}

{
    Name:        "replay_get_checkpoint",
    Description: "Get detailed information about a specific checkpoint.",
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "checkpoint_id": map[string]interface{}{
                "type":        "string",
                "description": "Checkpoint ID",
            },
        },
        "required": []string{"checkpoint_id"},
    },
}

{
    Name:        "replay_restore",
    Description: "Restore a session to a previous checkpoint. All changes after that checkpoint are lost.",
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "session_id": map[string]interface{}{
                "type":        "string",
                "description": "Session ID to restore",
            },
            "checkpoint_id": map[string]interface{}{
                "type":        "string",
                "description": "Checkpoint to restore to",
            },
        },
        "required": []string{"session_id", "checkpoint_id"},
    },
}

{
    Name:        "replay_fork",
    Description: "Create a new session starting from a checkpoint. Original session is unchanged.",
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "checkpoint_id": map[string]interface{}{
                "type":        "string",
                "description": "Checkpoint to fork from",
            },
            "name": map[string]interface{}{
                "type":        "string",
                "description": "Name for the new forked session",
            },
        },
        "required": []string{"checkpoint_id"},
    },
}
```

---

## Implementation Plan

### Phase 1: Core Infrastructure (Week 1)

- [ ] Create `replay_checkpoints` table migration
- [ ] Implement `internal/replay/` package
  - [ ] `Manager` interface
  - [ ] Postgres-backed implementation
  - [ ] Checkpoint CRUD operations
- [ ] Unit tests for replay manager

### Phase 2: Runner Integration (Week 1-2)

- [ ] Modify `internal/runner/runner.go` to create checkpoints
- [ ] Add `enable_replay` flag to session creation
- [ ] Add `checkpoint_id` to run response
- [ ] Integration tests for checkpoint creation during runs

### Phase 3: Restore & Fork Logic (Week 2)

- [ ] Implement `RestoreCheckpoint()` — restore workspace from snapshot
- [ ] Implement `ForkFromCheckpoint()` — create new session from snapshot
- [ ] Handle edge cases (concurrent restores, deleted snapshots)
- [ ] Integration tests for restore/fork operations

### Phase 4: API & MCP (Week 2-3)

- [ ] Add REST API endpoints (`/checkpoints`, `/restore`, `/fork`)
- [ ] Add MCP tools (4 new tools)
- [ ] Update OpenAPI spec
- [ ] API integration tests

### Phase 5: Dashboard UI (Week 3)

- [ ] Add "Checkpoints" tab to session detail page
- [ ] Timeline visualization of checkpoints
- [ ] Restore/fork buttons with confirmation dialogs
- [ ] Show diffs between checkpoints (file changes)

### Phase 6: Optimization & Polish (Week 3)

- [ ] Implement checkpoint pruning (auto-delete old checkpoints)
- [ ] Add retention policy config (`REPLAY_MAX_CHECKPOINTS_PER_SESSION`)
- [ ] Performance optimization (lazy snapshot loading)
- [ ] Documentation updates

---

## Configuration

### Environment Variables

```bash
# Enable replay feature globally
REPLAY_ENABLED=true

# Max checkpoints per session (auto-prune older ones)
REPLAY_MAX_CHECKPOINTS_PER_SESSION=50

# Storage backend for snapshots (existing)
SNAPSHOT_STORAGE=s3  # or 'local'
S3_BUCKET=vaultrun-snapshots
```

### Session-Level Config

```json
POST /api/v1/sessions
{
  "name": "debug-session",
  "image": "python:3.12-slim",
  "enable_replay": true,
  "replay_config": {
    "checkpoint_every_command": true,
    "max_checkpoints": 20
  }
}
```

---

## API Examples

### List Checkpoints

```bash
GET /api/v1/sessions/sess_abc123/checkpoints

Response:
{
  "checkpoints": [
    {
      "id": "cp_001",
      "session_id": "sess_abc123",
      "checkpoint_number": 0,
      "description": "Initial state",
      "created_at": "2026-07-30T17:00:00Z",
      "size_bytes": 45000000
    },
    {
      "id": "cp_002",
      "session_id": "sess_abc123",
      "run_id": "run_xyz",
      "checkpoint_number": 1,
      "command": "pip install requests",
      "exit_code": 0,
      "duration_ms": 2400,
      "description": "pip install requests — success in 2400ms",
      "created_at": "2026-07-30T17:00:05Z",
      "size_bytes": 52000000
    }
  ]
}
```

### Restore to Checkpoint

```bash
POST /api/v1/sessions/sess_abc123/restore
{
  "checkpoint_id": "cp_001"
}

Response:
{
  "session_id": "sess_abc123",
  "restored_to_checkpoint": "cp_001",
  "checkpoint_number": 1,
  "new_checkpoint_count": 2  # Checkpoints after cp_001 are preserved but session is at cp_001
}
```

### Fork from Checkpoint

```bash
POST /api/v1/checkpoints/cp_002/fork
{
  "name": "debug-fork-attempt-2"
}

Response:
{
  "session": {
    "id": "sess_new456",
    "name": "debug-fork-attempt-2",
    "status": "running",
    "forked_from_checkpoint": "cp_002",
    "parent_session": "sess_abc123"
  }
}
```

---

## MCP Tool Examples

### Agent Workflow: Debug a Failed Script

```python
# Agent runs a script that fails
result = run_command(session_id, "python", ["script.py"])
# exit_code: 1, error in output

# Agent lists checkpoints to see history
checkpoints = replay_list_checkpoints(session_id)
# Returns: [cp_0: initial, cp_1: deps installed, cp_2: script failed]

# Agent restores to before the failed run
replay_restore(session_id, checkpoints[1]["id"])

# Agent modifies the script
upload_file(session_id, "script.py", fixed_content)

# Agent re-runs from the restored state
result = run_command(session_id, "python", ["script.py"])
# Success!
```

### Agent Workflow: Test Multiple Approaches

```python
# Starting from a clean environment with dependencies installed
base_checkpoint = replay_list_checkpoints(session_id)[1]

# Try approach A
run_command(session_id, "python", ["approach_a.py"])
results_a = read_file(session_id, "output.txt")

# Restore to base
replay_restore(session_id, base_checkpoint["id"])

# Try approach B
run_command(session_id, "python", ["approach_b.py"])
results_b = read_file(session_id, "output.txt")

# Compare results and pick the best
```

---

## Storage Considerations

### Snapshot Size Management

**Problem:** Checkpoints can consume significant storage (50+ MB per checkpoint × 20 checkpoints = 1 GB per session).

**Solutions:**

1. **Delta snapshots** — Only store diffs between checkpoints
   - Use `rsync --only-write-batch` or similar
   - Store base snapshot + deltas
   - Reconstruct checkpoint by applying deltas

2. **Compression** — gzip/zstd snapshots before storage
   - Reduces size by 60-80% for typical workspaces
   - Trade-off: CPU time on restore

3. **Deduplication** — Content-addressable storage
   - Files with same hash stored once
   - Workspace manifests point to shared blobs

4. **Smart pruning** — Keep important checkpoints
   - Always keep: initial, last, and every Nth
   - Delete checkpoints from successful runs older than X days
   - Keep all checkpoints from failed runs (debugging value)

### Recommended Approach for MVP

Start simple:
- Full snapshots with gzip compression
- Prune after 50 checkpoints per session
- Add delta snapshots in future if storage becomes issue

---

## Performance Considerations

### Checkpoint Creation Overhead

**Target:** < 500ms additional latency per command

**Optimizations:**

1. **Async checkpoint creation** — Don't block command response
   ```go
   go func() {
       checkpoint, err := createCheckpoint(...)
       if err != nil {
           log.Error("async checkpoint failed", err)
       }
   }()
   return runResult // return immediately
   ```

2. **Lazy snapshot upload** — Return response before S3 upload completes
   - Snapshot to local disk first (fast)
   - Upload to S3 in background
   - Mark checkpoint as "pending" until upload done

3. **Selective checkpointing** — Only checkpoint on important commands
   - Skip checkpoints for read-only commands (ls, cat, etc.)
   - Checkpoint only on write operations or on-demand

### Restore Performance

**Target:** < 5 seconds to restore a 500MB workspace

**Optimizations:**

1. **Parallel download** — Stream snapshot while extracting
2. **Local cache** — Keep recent snapshots on local disk
3. **Resume capability** — If restore fails, resume download

---

## Security & Audit

### Audit Log Integration

All replay operations are logged:

```json
{
  "event": "checkpoint_created",
  "session_id": "sess_abc123",
  "checkpoint_id": "cp_002",
  "checkpoint_number": 1,
  "actor": "agent_xyz",
  "size_bytes": 52000000
}

{
  "event": "session_restored",
  "session_id": "sess_abc123",
  "checkpoint_id": "cp_001",
  "restored_by": "user@example.com",
  "timestamp": "2026-07-30T17:05:00Z"
}

{
  "event": "checkpoint_forked",
  "source_checkpoint": "cp_002",
  "new_session_id": "sess_new456",
  "forked_by": "agent_xyz"
}
```

### RBAC Considerations

- **Viewer role** — Can list and view checkpoints, cannot restore/fork
- **Executor role** — Can restore and fork within own org sessions
- **Admin role** — Full access including pruning checkpoints

---

## Testing Strategy

### Unit Tests

- [ ] `internal/replay/manager_test.go` — CRUD operations
- [ ] Checkpoint creation with various command outcomes
- [ ] Pruning logic (keep last N, delete old)

### Integration Tests

- [ ] Full flow: create session → run commands → checkpoints created
- [ ] Restore checkpoint → verify workspace state matches
- [ ] Fork checkpoint → verify new session is independent
- [ ] Concurrent checkpoint creation (race conditions)

### E2E Tests

- [ ] MCP tools integration
- [ ] Dashboard UI for checkpoint management
- [ ] Large workspace restore (500MB+)
- [ ] Checkpoint pruning with retention policy

---

## Documentation Updates

### Files to Update

- [ ] `README.md` — Add replay feature to "What's included"
- [ ] `docs/architecture.md` — Add replay subsystem diagram
- [ ] `docs/mcp.md` — Document new MCP tools (replay_*)
- [ ] `docs/openapi.yaml` — Add new API endpoints
- [ ] `CHANGELOG.md` — Add feature to next version

### New Documentation

- [ ] `docs/replay-guide.md` — User guide with examples
- [ ] `docs/features/session-replay.md` — This spec document
- [ ] Dashboard in-app help tooltips

---

## Future Enhancements

### Phase 2 Features (Post-MVP)

1. **Checkpoint diffing** — Show file changes between checkpoints
   ```bash
   GET /api/v1/checkpoints/cp_002/diff?compare_to=cp_001
   ```

2. **Replay with modifications** — Replay a command with different args
   ```bash
   POST /api/v1/checkpoints/cp_001/replay
   {"command": "python", "args": ["script.py", "--verbose"]}
   ```

3. **Checkpoint branching visualization** — Tree view in dashboard
   ```
   cp_0 ─┬─ cp_1 ─── cp_2 ─── cp_3
         │
         └─ cp_1b ─── cp_2b (forked)
   ```

4. **Checkpoint sharing** — Export/import checkpoints between sessions
5. **Smart checkpoint naming** — LLM-generated descriptions based on file changes
6. **Checkpoint tags** — "before_deploy", "working_state", "bug_reproduced"

---

## Success Metrics

### Technical Metrics

- Checkpoint creation latency < 500ms (p95)
- Restore operation < 5 seconds for 500MB workspace (p95)
- Storage overhead < 2x session workspace size with compression

### Product Metrics

- % of sessions with replay enabled (target: >40% after 3 months)
- Avg checkpoints per session (target: 8-12)
- Restore operations per week (indicator of debugging usage)
- Feedback from early users on debugging workflow improvement

---

## Questions & Decisions

### Open Questions

1. **Should we checkpoint on every command by default?**
   - Pro: Complete history, no missed states
   - Con: Storage overhead, slower commands
   - **Recommendation:** Yes for MVP, add selective checkpointing later

2. **How long to retain checkpoints?**
   - Option A: Delete when session is deleted (same lifecycle)
   - Option B: Retain for X days after session deletion (forensics)
   - **Recommendation:** Option A for MVP, Option B for Enterprise

3. **Should forked sessions share snapshots?**
   - Pro: Saves storage (CoW-style)
   - Con: Can't delete parent checkpoint if child depends on it
   - **Recommendation:** Copy on fork for MVP (simpler), optimize later

---

## References

- Existing snapshot implementation: `internal/snapshot/`
- Audit logging: `internal/audit/`
- Docker exec flow: `internal/docker/exec.go`
- S3 storage: `internal/storage/s3.go`

---

## Contact

Voor vragen over deze spec:
- GitHub Issue: Label met `feature: session-replay`
- Mail: mail@030.dev
- Discussie: [GitHub Discussions](https://github.com/nickvd7/vaultrun/discussions)
