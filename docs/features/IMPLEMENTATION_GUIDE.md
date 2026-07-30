# Implementation Guide - Killer Features

Praktische gids voor het implementeren van de 6 killer features. Per feature: concrete stappen, code templates, en common pitfalls.

---

## Quick Start

### Voor Implementors

1. **Kies een feature** uit priority list
2. **Lees de spec** in `docs/features/[feature-name].md`
3. **Review security** in `docs/features/SECURITY_REVIEW.md`
4. **Follow deze guide** voor step-by-step implementatie
5. **Run tests** uit de spec's testing checklist

### Voor Project Managers

- **Effort estimates** zijn conservatief (include testing + security)
- **Dependencies** zijn gedocumenteerd per feature
- **Parallel work** mogelijk: Browser + Templates kunnen tegelijk
- **Risk assessment** staat in elke spec

---

## Feature 1: Session Replay (2-3 weken)

### Prerequisites

```bash
# Existing infrastructure
✓ internal/snapshot/ - Snapshot creation and storage
✓ internal/docker/ - Container management
✓ internal/audit/ - Audit logging

# New dependencies
go get github.com/minio/sha256-simd  # Fast hashing
```

### Week 1: Core Infrastructure

#### Day 1-2: Database Schema

```bash
# Create migration
migrate create -ext sql -dir migrations -seq add_replay_checkpoints

# migrations/XXX_add_replay_checkpoints.up.sql
```

```sql
CREATE TABLE replay_checkpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id UUID REFERENCES runs(id) ON DELETE SET NULL,
    checkpoint_number INT NOT NULL,
    workspace_snapshot_id UUID NOT NULL REFERENCES snapshots(id),
    env_vars_snapshot JSONB,
    command TEXT,
    args JSONB,
    exit_code INT,
    duration_ms INT,
    stdout_preview TEXT,
    stderr_preview TEXT,
    signature VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    size_bytes BIGINT,
    UNIQUE(session_id, checkpoint_number)
);

CREATE INDEX idx_replay_checkpoints_session ON replay_checkpoints(session_id, checkpoint_number DESC);
```

**Testing:**
```bash
# Run migration
make migrate-up

# Verify table
psql $DATABASE_URL -c "\d replay_checkpoints"
```

#### Day 3-4: Replay Manager Package

```bash
mkdir -p internal/replay
touch internal/replay/manager.go
touch internal/replay/manager_test.go
```

**Template: `internal/replay/manager.go`**

```go
package replay

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "time"

    "github.com/google/uuid"
    "github.com/nickvd7/vaultrun/internal/snapshot"
)

type Manager struct {
    db         *sql.DB
    snapshots  snapshot.Manager
    signingKey []byte
}

func NewManager(db *sql.DB, snapshots snapshot.Manager, signingKey []byte) *Manager {
    return &Manager{
        db:         db,
        snapshots:  snapshots,
        signingKey: signingKey,
    }
}

func (m *Manager) CreateCheckpoint(ctx context.Context, opts CreateCheckpointOpts) (*Checkpoint, error) {
    // 1. Get next checkpoint number
    number, err := m.getNextCheckpointNumber(ctx, opts.SessionID)
    if err != nil {
        return nil, err
    }
    
    // 2. Create workspace snapshot
    snapshotID, size, err := m.snapshots.CreateSnapshot(ctx, opts.SessionID)
    if err != nil {
        return nil, err
    }
    
    // 3. Create checkpoint record
    checkpoint := &Checkpoint{
        ID:                uuid.New(),
        SessionID:         opts.SessionID,
        RunID:             opts.RunID,
        Number:            number,
        SnapshotID:        snapshotID,
        EnvVars:           opts.EnvVars,
        Command:           opts.Command,
        Args:              opts.Args,
        ExitCode:          opts.ExitCode,
        DurationMs:        opts.DurationMs,
        StdoutPreview:     truncate(opts.Stdout, 500),
        StderrPreview:     truncate(opts.Stderr, 500),
        SizeBytes:         size,
        CreatedAt:         time.Now(),
    }
    
    // 4. Sign checkpoint
    checkpoint.Signature = m.signCheckpoint(checkpoint)
    
    // 5. Store in database
    if err := m.db.InsertCheckpoint(ctx, checkpoint); err != nil {
        return nil, err
    }
    
    return checkpoint, nil
}

func (m *Manager) signCheckpoint(cp *Checkpoint) string {
    data := fmt.Sprintf("%s:%s:%d:%d",
        cp.SessionID, cp.SnapshotID, cp.Number, cp.CreatedAt.Unix())
    
    h := hmac.New(sha256.New, m.signingKey)
    h.Write([]byte(data))
    return hex.EncodeToString(h.Sum(nil))
}
```

**Testing:**
```bash
go test ./internal/replay/... -v
```

#### Day 5: Runner Integration

**Modify: `internal/runner/runner.go`**

```go
type Runner struct {
    docker      *docker.Client
    replayMgr   *replay.Manager  // NEW
    // ... existing fields
}

func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Run, error) {
    // Existing pre-run logic...
    
    // NEW: Create checkpoint if enabled
    if opts.EnableReplay {
        checkpoint, err := r.replayMgr.CreateCheckpoint(ctx, replay.CreateCheckpointOpts{
            SessionID:  opts.SessionID,
            RunID:      &result.ID,
            Command:    opts.Command,
            Args:       opts.Args,
            ExitCode:   &result.ExitCode,
            DurationMs: &result.DurationMs,
            Stdout:     result.Stdout,
            Stderr:     result.Stderr,
        })
        
        if err != nil {
            log.Warn("Checkpoint creation failed", "error", err)
        } else {
            result.CheckpointID = &checkpoint.ID
        }
    }
    
    return result, nil
}
```

**Testing:**
```bash
# Integration test
go test ./internal/runner/... -run TestRunWithReplay -v
```

### Week 2: Restore & Fork

#### Day 1-3: Restore Logic

```go
func (m *Manager) RestoreCheckpoint(ctx context.Context, sessionID, checkpointID uuid.UUID) error {
    // 1. Verify no active commands
    activeRuns := m.db.GetActiveRuns(ctx, sessionID)
    if len(activeRuns) > 0 {
        return ErrActiveCommandsRunning
    }
    
    // 2. Get checkpoint
    cp := m.db.GetCheckpoint(ctx, checkpointID)
    if cp == nil {
        return ErrCheckpointNotFound
    }
    
    // 3. Verify signature
    if !m.verifyCheckpoint(cp) {
        return ErrCheckpointTampered
    }
    
    // 4. Restore snapshot
    if err := m.snapshots.RestoreSnapshot(ctx, sessionID, cp.SnapshotID); err != nil {
        return err
    }
    
    // 5. Restore env vars
    if err := m.docker.SetEnvVars(ctx, sessionID, cp.EnvVars); err != nil {
        return err
    }
    
    return nil
}
```

#### Day 4-5: Fork Logic

```go
func (m *Manager) ForkFromCheckpoint(ctx context.Context, checkpointID uuid.UUID, newSessionName string) (*Session, error) {
    // 1. Get checkpoint
    cp := m.db.GetCheckpoint(ctx, checkpointID)
    
    // 2. Get original session config
    originalSession := m.db.GetSession(ctx, cp.SessionID)
    
    // 3. Create new session with same config
    newSession := &Session{
        Name:           newSessionName,
        Image:          originalSession.Image,
        CPULimit:       originalSession.CPULimit,
        MemoryLimitMB:  originalSession.MemoryLimitMB,
        ForkedFromCheckpoint: &cp.ID,
    }
    
    if err := m.db.CreateSession(ctx, newSession); err != nil {
        return nil, err
    }
    
    // 4. Restore checkpoint to new session
    if err := m.RestoreCheckpoint(ctx, newSession.ID, checkpointID); err != nil {
        m.db.DeleteSession(ctx, newSession.ID)
        return nil, err
    }
    
    return newSession, nil
}
```

### Week 3: API, MCP, Dashboard

#### Day 1-2: REST API

```go
// cmd/api/handlers/replay.go
func (h *ReplayHandler) ListCheckpoints(c *gin.Context) {
    sessionID := c.Param("id")
    checkpoints := h.replayMgr.ListCheckpoints(c.Request.Context(), uuid.MustParse(sessionID))
    c.JSON(200, gin.H{"checkpoints": checkpoints})
}

func (h *ReplayHandler) RestoreCheckpoint(c *gin.Context) {
    var req struct {
        CheckpointID string `json:"checkpoint_id" binding:"required"`
    }
    
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    sessionID := c.Param("id")
    err := h.replayMgr.RestoreCheckpoint(
        c.Request.Context(),
        uuid.MustParse(sessionID),
        uuid.MustParse(req.CheckpointID),
    )
    
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    c.JSON(200, gin.H{"status": "restored"})
}
```

#### Day 3-4: MCP Tools

```go
// sdk/mcp/tools.go
toolDefinitions = append(toolDefinitions, MCPTool{
    Name:        "replay_list_checkpoints",
    Description: "List all replay checkpoints for a session",
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "session_id": map[string]interface{}{
                "type":        "string",
                "description": "Session ID",
            },
        },
        "required": []string{"session_id"},
    },
})

// sdk/mcp/replay.go
func handleReplayListCheckpoints(args map[string]interface{}) (interface{}, error) {
    sessionID := args["session_id"].(string)
    
    resp, err := apiClient.Get(fmt.Sprintf("/api/v1/sessions/%s/checkpoints", sessionID))
    if err != nil {
        return nil, err
    }
    
    return resp, nil
}
```

#### Day 5: Dashboard UI

```tsx
// apps/frontend/components/SessionCheckpoints.tsx
export function SessionCheckpoints({ sessionId }: { sessionId: string }) {
  const { data: checkpoints } = useQuery(['checkpoints', sessionId], () =>
    api.get(`/sessions/${sessionId}/checkpoints`)
  );

  const restoreMutation = useMutation((checkpointId: string) =>
    api.post(`/sessions/${sessionId}/restore`, { checkpoint_id: checkpointId })
  );

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold">Checkpoints</h3>
      {checkpoints?.map((cp) => (
        <div key={cp.id} className="border rounded p-4">
          <div className="flex justify-between">
            <div>
              <p className="font-medium">Checkpoint #{cp.checkpoint_number}</p>
              <p className="text-sm text-gray-600">{cp.description}</p>
              <p className="text-xs text-gray-500">{formatDate(cp.created_at)}</p>
            </div>
            <button
              onClick={() => restoreMutation.mutate(cp.id)}
              className="btn-primary"
            >
              Restore
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}
```

### Testing Checklist

```bash
# Unit tests
✓ Checkpoint creation
✓ Checkpoint signing/verification
✓ Restore logic
✓ Fork logic

# Integration tests
✓ Full workflow: create session → run commands → create checkpoints → restore
✓ Concurrent checkpoint creation
✓ Restore during active command (should fail)
✓ Fork from checkpoint

# E2E tests
✓ Dashboard checkpoint list
✓ Dashboard restore button
✓ MCP tool integration

# Security tests
✓ Sensitive file exclusion (.ssh/, .aws/)
✓ Checkpoint signature verification
✓ Storage limits enforcement

# Performance tests
✓ Large workspace (1GB+) checkpoint creation
✓ Restore performance
✓ Storage cleanup
```

---

## Feature 2: Browser Automation (1-2 weken)

### Week 1: Docker Images & Core

#### Day 1: Docker Images

```dockerfile
# deployments/docker/browser/Dockerfile.playwright-python
FROM python:3.12-slim

# Install system dependencies
RUN apt-get update && apt-get install -y \
    wget \
    gnupg \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Playwright
RUN pip install playwright==1.40.0

# Install Chromium
RUN playwright install chromium
RUN playwright install-deps chromium

# Security: Run as non-root
RUN useradd -m -s /bin/bash browser
USER browser
WORKDIR /workspace

CMD ["python"]
```

```bash
# Build and push
docker build -t vaultrun/browser:playwright-python -f deployments/docker/browser/Dockerfile.playwright-python .
docker push vaultrun/browser:playwright-python
```

#### Day 2-3: Browser Manager

```go
// internal/browser/manager.go
package browser

type Manager struct {
    docker    *docker.Client
    storage   ArtifactStorage
    policy    NetworkPolicy
}

func (m *Manager) Navigate(ctx context.Context, sessionID uuid.UUID, url string, opts NavigateOpts) error {
    // 1. Validate URL (SSRF protection)
    if err := m.validateURL(url); err != nil {
        return err
    }
    
    // 2. Execute Playwright script
    script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page()
    page.goto('%s')
    page.wait_for_load_state('%s')
    browser.close()
`, url, opts.WaitUntil)
    
    // 3. Run in container
    result, err := m.docker.Exec(ctx, sessionID, "python", []string{"-c", script})
    return err
}

func (m *Manager) validateURL(url string) error {
    parsed, _ := url.Parse(url)
    
    // Resolve to IP
    ips, _ := net.LookupIP(parsed.Hostname())
    
    for _, ip := range ips {
        // Block private IPs
        if isPrivateIP(ip) {
            return errors.New("private IP blocked")
        }
        
        // Block cloud metadata
        if ip.String() == "169.254.169.254" {
            return errors.New("cloud metadata blocked")
        }
    }
    
    return nil
}
```

#### Day 4-5: MCP Tools

```go
// sdk/mcp/browser.go
func handleBrowserNavigate(args map[string]interface{}) (interface{}, error) {
    sessionID := args["session_id"].(string)
    url := args["url"].(string)
    
    resp, err := apiClient.Post(fmt.Sprintf("/api/v1/sessions/%s/browser/navigate", sessionID), map[string]interface{}{
        "url": url,
    })
    
    return resp, err
}

func handleBrowserScreenshot(args map[string]interface{}) (interface{}, error) {
    sessionID := args["session_id"].(string)
    fullPage := args["full_page"].(bool)
    
    // Playwright screenshot script
    script := `
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page()
    screenshot = page.screenshot(full_page=%v)
    print(base64.b64encode(screenshot).decode())
    browser.close()
`
    
    // Execute and save as artifact
    result := executeScript(sessionID, fmt.Sprintf(script, fullPage))
    artifact := saveArtifact(sessionID, "screenshot.png", result)
    
    return artifact, nil
}
```

### Testing

```bash
# Integration test
go test ./internal/browser/... -v

# E2E test
python sdk/python/examples/browser_scraping.py
```

---

## Feature 3-6: Quick Implementation Notes

### Multi-Agent Collaboration (4-6 weken)

**Week 1:** WebSocket server + Redis pub/sub  
**Week 2:** File sync with versioning  
**Week 3:** Messaging + presence  
**Week 4:** Dashboard UI + testing  

**Key Files:**
- `internal/collaboration/server.go` - WebSocket handler
- `internal/collaboration/filesync.go` - File versioning
- `internal/collaboration/presence.go` - Agent tracking

### Natural Language Policy (2-3 weken)

**Week 1:** LLM integration + prompt templates  
**Week 2:** Policy compiler (JSON → OPA/iptables)  
**Week 3:** API + MCP tools + UI  

**Key Files:**
- `internal/nlpolicy/parser.go` - LLM integration
- `internal/nlpolicy/compiler.go` - Policy generation
- `internal/nlpolicy/validator.go` - Safety checks

### Cost Intelligence (2-3 weken)

**Week 1:** Metrics collection  
**Week 2:** Cost calculator + analytics  
**Week 3:** Dashboard UI + reports  

**Key Files:**
- `internal/cost/collector.go` - Docker stats polling
- `internal/cost/calculator.go` - Cost computation
- `internal/cost/optimizer.go` - Recommendations

### Session Templates (2-3 weken)

**Week 1:** Template schema + validation  
**Week 2:** Template marketplace API  
**Week 3:** Dashboard UI + image building  

**Key Files:**
- `internal/templates/manager.go` - CRUD operations
- `internal/templates/validator.go` - Security scanning
- `deployments/docker/templates/` - Official images

---

## Common Pitfalls & Solutions

### Pitfall 1: Race Conditions

**Problem:** Two goroutines modify same resource  
**Solution:** Use mutexes or database transactions

```go
// Bad
func increment(counter *int) {
    *counter++  // NOT thread-safe
}

// Good
type SafeCounter struct {
    mu    sync.Mutex
    count int
}

func (c *SafeCounter) Increment() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.count++
}
```

### Pitfall 2: SQL Injection

**Problem:** String concatenation in queries  
**Solution:** Always use parameterized queries

```go
// Bad
query := fmt.Sprintf("SELECT * FROM users WHERE id = '%s'", userID)

// Good
query := "SELECT * FROM users WHERE id = $1"
db.Query(query, userID)
```

### Pitfall 3: Missing Authorization

**Problem:** API endpoints zonder auth check  
**Solution:** Middleware op alle endpoints

```go
// Apply auth middleware
authorized := router.Group("/api/v1", AuthMiddleware())
authorized.GET("/sessions/:id", GetSession)
```

### Pitfall 4: Unbounded Resources

**Problem:** No limits on user input  
**Solution:** Always set limits

```go
// Max file size
const MaxUploadSize = 100 * 1024 * 1024  // 100MB

// Max array length
if len(items) > 1000 {
    return errors.New("too many items")
}

// Timeout on external calls
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```

### Pitfall 5: Sensitive Data in Logs

**Problem:** API keys in error messages  
**Solution:** Redact before logging

```go
func redactAPIKey(s string) string {
    re := regexp.MustCompile(`sk-[a-zA-Z0-9]{48}`)
    return re.ReplaceAllString(s, "[REDACTED]")
}

log.Error("API call failed", "error", redactAPIKey(err.Error()))
```

---

## Performance Optimization Tips

### Database

```go
// Use prepared statements
stmt, _ := db.Prepare("SELECT * FROM users WHERE id = $1")
defer stmt.Close()

// Batch inserts
tx, _ := db.Begin()
for _, item := range items {
    tx.Exec("INSERT INTO items VALUES ($1)", item)
}
tx.Commit()

// Connection pooling
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(5 * time.Minute)
```

### Caching

```go
// In-memory cache
cache := make(map[string]interface{})
cacheMu := sync.RWMutex{}

func GetCached(key string) (interface{}, bool) {
    cacheMu.RLock()
    defer cacheMu.RUnlock()
    val, ok := cache[key]
    return val, ok
}

// Redis cache
rdb := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

rdb.Set(ctx, "key", "value", 10*time.Minute)
```

### Concurrency

```go
// Worker pool pattern
jobs := make(chan Job, 100)
results := make(chan Result, 100)

// Start workers
for w := 0; w < 10; w++ {
    go worker(jobs, results)
}

// Send jobs
for _, job := range jobList {
    jobs <- job
}
close(jobs)

// Collect results
for r := range results {
    handleResult(r)
}
```

---

## Monitoring & Observability

### Metrics

```go
import "github.com/prometheus/client_golang/prometheus"

var (
    checkpointsCreated = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "vaultrun_checkpoints_created_total",
            Help: "Total checkpoints created",
        },
    )
    
    checkpointSize = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "vaultrun_checkpoint_size_bytes",
            Help:    "Checkpoint size distribution",
            Buckets: prometheus.ExponentialBuckets(1024, 2, 20),
        },
    )
)

func init() {
    prometheus.MustRegister(checkpointsCreated)
    prometheus.MustRegister(checkpointSize)
}

// In your code
checkpointsCreated.Inc()
checkpointSize.Observe(float64(size))
```

### Logging

```go
import "go.uber.org/zap"

logger, _ := zap.NewProduction()
defer logger.Sync()

logger.Info("checkpoint created",
    zap.String("session_id", sessionID.String()),
    zap.Int("checkpoint_number", number),
    zap.Int64("size_bytes", size),
)
```

### Tracing

```go
import "go.opentelemetry.io/otel"

tracer := otel.Tracer("vaultrun")

ctx, span := tracer.Start(ctx, "CreateCheckpoint")
defer span.End()

span.SetAttributes(
    attribute.String("session.id", sessionID.String()),
    attribute.Int("checkpoint.number", number),
)
```

---

## Resources

### Documentation
- VaultRun Specs: `docs/features/*.md`
- Security Review: `docs/features/SECURITY_REVIEW.md`
- Architecture: `docs/architecture.md`

### External
- Go Best Practices: https://go.dev/doc/effective_go
- Docker API: https://docs.docker.com/engine/api/
- Playwright: https://playwright.dev/python/
- OpenTelemetry: https://opentelemetry.io/docs/

### Support
- GitHub Issues: https://github.com/nickvd7/vaultrun/issues
- Team: mail@030.dev
