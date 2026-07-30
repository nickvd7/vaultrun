# Browser Automation Layer

## Executive Summary

Headless browser support in VaultRun sandboxes met Playwright/Puppeteer pre-installed, screenshot/video capture, en dedicated MCP tools. Maakt VaultRun het platform voor web scraping agents en E2E testing automation.

**Status:** 🎯 Prioriteit 1  
**Effort:** Small-Medium (1-2 weken)  
**Dependencies:** Chrome/Chromium in Docker images

---

## Problem Statement

**Current state:** Agents die web scraping of browser testing willen doen moeten:
1. Handmatig Playwright/Puppeteer installeren in de sandbox
2. Chrome/Chromium dependencies resolven (vaak 200+ MB download)
3. Zelf screenshots/videos opslaan en exporteren
4. Complexe browser automation code schrijven

**Impact:** Web automation is een top-3 use case voor agents, maar VaultRun maakt het niet makkelijk.

---

## Solution Overview

### Features

1. **Pre-built browser images** — `vaultrun/browser:latest` met Playwright + Chrome
2. **MCP tools** — High-level browser automation tools
3. **Artifact capture** — Auto-save screenshots, videos, traces
4. **Network inspection** — Capture HAR files, block resources
5. **Multi-page support** — Meerdere tabs/pages per session

### Docker Images

**New images:**

```dockerfile
# vaultrun/browser:playwright-python
FROM python:3.12-slim
RUN pip install playwright && playwright install chromium

# vaultrun/browser:playwright-node
FROM node:20-slim
RUN npm install -g playwright && playwright install chromium

# vaultrun/browser:puppeteer
FROM node:20-slim
RUN npm install -g puppeteer
```

Images published to Docker Hub: `vaultrun/browser:*`

---

## MCP Tools

### New Tools (8 tools)

```go
{
    Name:        "browser_navigate",
    Description: "Navigate to a URL in the browser",
    InputSchema: {
        "session_id": "string (required)",
        "url": "string (required)",
        "wait_until": "load|domcontentloaded|networkidle (optional)"
    },
}

{
    Name:        "browser_screenshot",
    Description: "Take a screenshot of the current page",
    InputSchema: {
        "session_id": "string (required)",
        "full_page": "boolean (default: false)",
        "selector": "string (optional) - screenshot specific element"
    },
    Output: {
        "artifact_id": "uuid",
        "url": "https://vaultrun.dev/artifacts/screenshot.png"
    }
}

{
    Name:        "browser_click",
    Description: "Click an element on the page",
    InputSchema: {
        "session_id": "string (required)",
        "selector": "string (required) - CSS selector"
    },
}

{
    Name:        "browser_fill",
    Description: "Fill an input field",
    InputSchema: {
        "session_id": "string (required)",
        "selector": "string (required)",
        "value": "string (required)"
    },
}

{
    Name:        "browser_extract",
    Description: "Extract text or HTML from the page",
    InputSchema: {
        "session_id": "string (required)",
        "selector": "string (optional) - extract from specific element",
        "extract": "text|html|attributes (default: text)"
    },
}

{
    Name:        "browser_evaluate",
    Description: "Run JavaScript in the browser context",
    InputSchema: {
        "session_id": "string (required)",
        "script": "string (required) - JavaScript code"
    },
}

{
    Name:        "browser_wait",
    Description: "Wait for an element or condition",
    InputSchema: {
        "session_id": "string (required)",
        "selector": "string (optional) - wait for element",
        "timeout": "number (ms, default: 30000)"
    },
}

{
    Name:        "browser_pdf",
    Description: "Generate PDF of the current page",
    InputSchema: {
        "session_id": "string (required)",
        "format": "A4|Letter (default: A4)"
    },
}
```

---

## Implementation

### Architecture

```
┌─────────────────────────────────────────┐
│ MCP Tool: browser_navigate              │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│ Browser Manager (internal/browser/)     │
│  - Maintain Playwright process per sess │
│  - Connection pooling                   │
│  - Auto-cleanup on session end          │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│ Docker Container (vaultrun/browser)     │
│  - Playwright Python/Node               │
│  - Chromium browser                     │
│  - Browser runs in sandbox              │
└─────────────────────────────────────────┘
```

### New Package: `internal/browser/`

```go
package browser

type Manager interface {
    // Initialize browser in session
    Init(ctx context.Context, sessionID uuid.UUID) error
    
    // Navigate to URL
    Navigate(ctx context.Context, sessionID uuid.UUID, url string, opts NavigateOpts) error
    
    // Take screenshot
    Screenshot(ctx context.Context, sessionID uuid.UUID, opts ScreenshotOpts) (*Artifact, error)
    
    // Click element
    Click(ctx context.Context, sessionID uuid.UUID, selector string) error
    
    // Fill input
    Fill(ctx context.Context, sessionID uuid.UUID, selector, value string) error
    
    // Extract content
    Extract(ctx context.Context, sessionID uuid.UUID, opts ExtractOpts) (string, error)
    
    // Evaluate JavaScript
    Evaluate(ctx context.Context, sessionID uuid.UUID, script string) (interface{}, error)
    
    // Wait for element
    Wait(ctx context.Context, sessionID uuid.UUID, opts WaitOpts) error
    
    // Generate PDF
    PDF(ctx context.Context, sessionID uuid.UUID, opts PDFOpts) (*Artifact, error)
    
    // Cleanup browser resources
    Close(ctx context.Context, sessionID uuid.UUID) error
}

type PlaywrightManager struct {
    docker *docker.Client
    storage ArtifactStorage
}

func (m *PlaywrightManager) Navigate(ctx context.Context, sessionID uuid.UUID, url string, opts NavigateOpts) error {
    // Execute Python script in container
    script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page()
    page.goto('%s')
    page.wait_for_load_state('%s')
    browser.close()
`, url, opts.WaitUntil)
    
    return m.docker.Exec(ctx, sessionID, "python", []string{"-c", script})
}
```

### Implementation Approach

**Option A: Python scripts executed via Docker exec (Recommended)**
- Simple to implement
- No persistent state needed
- Each tool call = one Python script execution
- Good for MVP

**Option B: Long-running Playwright server**
- Playwright runs as daemon in container
- Tools communicate via HTTP/WebSocket
- Better performance (no startup cost)
- More complex (process management, health checks)

**Recommendation:** Start with Option A, migrate to Option B if performance is issue.

---

## Phase 1: Docker Images (Week 1)

- [ ] Create `vaultrun/browser:playwright-python` Dockerfile
- [ ] Create `vaultrun/browser:playwright-node` Dockerfile
- [ ] Create `vaultrun/browser:puppeteer` Dockerfile
- [ ] Build and push to Docker Hub
- [ ] Add images to `VAULTRUN_DEFAULT_IMAGE` options

### Phase 2: Browser Manager (Week 1)

- [ ] Implement `internal/browser/manager.go`
- [ ] Implement core operations (navigate, screenshot, click, fill)
- [ ] Integration with artifact storage
- [ ] Unit tests with mock Docker client

### Phase 3: MCP Tools (Week 2)

- [ ] Add 8 browser tools to `sdk/mcp/tools.go`
- [ ] Implement tool handlers in `sdk/mcp/browser.go`
- [ ] Integration tests with real browser
- [ ] Update MCP README

### Phase 4: Documentation & Examples (Week 2)

- [ ] Add browser examples to `examples/browser-automation/`
- [ ] Update main README with browser use cases
- [ ] Create tutorial: "Build a web scraper in 5 minutes"
- [ ] Dashboard UI: show browser artifacts (screenshots) inline

---

## Usage Examples

### Example 1: Web Scraping

```python
from vaultrun_mcp import VaultRunClient

client = VaultRunClient(api_key="vr_...")
session = client.create_session(image="vaultrun/browser:playwright-python")

# Navigate to page
client.browser_navigate(session.id, "https://news.ycombinator.com")

# Extract headlines
headlines = client.browser_extract(
    session.id,
    selector=".storylink",
    extract="text"
)

# Take screenshot
screenshot = client.browser_screenshot(session.id, full_page=True)
print(f"Screenshot: {screenshot.url}")
```

### Example 2: E2E Testing

```python
# Login flow test
session = create_session(image="vaultrun/browser:playwright-python")

browser_navigate(session.id, "https://app.example.com/login")
browser_fill(session.id, "#email", "test@example.com")
browser_fill(session.id, "#password", "password123")
browser_click(session.id, "button[type=submit]")
browser_wait(session.id, selector="#dashboard", timeout=5000)

# Verify we're on dashboard
html = browser_extract(session.id, selector="h1", extract="text")
assert "Dashboard" in html

# Screenshot for report
screenshot = browser_screenshot(session.id)
```

### Example 3: PDF Generation

```python
# Generate PDF report
session = create_session(image="vaultrun/browser:playwright-python")

browser_navigate(session.id, "https://company.com/annual-report")
browser_wait(session.id, selector=".report-loaded")
pdf = browser_pdf(session.id, format="A4")

# Download PDF
download_artifact(pdf.id, "annual-report.pdf")
```

---

## Configuration

```bash
# Browser settings
BROWSER_ENABLED=true
BROWSER_DEFAULT_IMAGE=vaultrun/browser:playwright-python
BROWSER_TIMEOUT=60s  # Max time for browser operations
BROWSER_MAX_PAGES=5  # Max tabs/pages per session

# Screenshot/video settings
BROWSER_SCREENSHOT_MAX_SIZE=10MB
BROWSER_VIDEO_ENABLED=false  # Enable video recording (future)
```

---

## Security Considerations

### Critical Security Measures

#### SSRF Protection

Prevent agents from using browser to scan internal networks:

```go
type BrowserNetworkPolicy struct {
    AllowedHosts    []string
    BlockPrivateIPs bool  // Block 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
    BlockLocalhost  bool
    BlockMetadata   bool  // Block cloud metadata endpoints
}

func (b *BrowserManager) Navigate(sessionID uuid.UUID, url string) error {
    parsed, _ := url.Parse(url)
    hostname := parsed.Hostname()
    
    // Resolve hostname to IP
    ips, err := net.LookupIP(hostname)
    if err != nil {
        return fmt.Errorf("failed to resolve hostname: %w", err)
    }
    
    // Check each IP
    for _, ip := range ips {
        // Block private IPs
        if b.policy.BlockPrivateIPs && isPrivateIP(ip) {
            return errors.New("navigation to private IP blocked")
        }
        
        // Block localhost
        if b.policy.BlockLocalhost && ip.IsLoopback() {
            return errors.New("navigation to localhost blocked")
        }
        
        // Block cloud metadata endpoints
        if b.policy.BlockMetadata && isMetadataIP(ip) {
            return errors.New("navigation to cloud metadata endpoint blocked")
        }
    }
    
    // Check allowlist
    if len(b.policy.AllowedHosts) > 0 {
        if !contains(b.policy.AllowedHosts, hostname) {
            return errors.New("host not in allowlist")
        }
    }
    
    // Proceed with navigation
    return b.performNavigation(sessionID, url)
}

func isPrivateIP(ip net.IP) bool {
    privateRanges := []string{
        "10.0.0.0/8",
        "172.16.0.0/12",
        "192.168.0.0/16",
        "127.0.0.0/8",
        "169.254.0.0/16",  // Link-local
        "fd00::/8",         // IPv6 ULA
    }
    
    for _, cidr := range privateRanges {
        _, network, _ := net.ParseCIDR(cidr)
        if network.Contains(ip) {
            return true
        }
    }
    return false
}

func isMetadataIP(ip net.IP) bool {
    // AWS, GCP, Azure metadata endpoints
    metadataIPs := []string{
        "169.254.169.254",  // AWS, Azure
        "169.254.169.253",  // AWS IMDSv2
        "metadata.google.internal",  // GCP
    }
    
    ipStr := ip.String()
    for _, metaIP := range metadataIPs {
        if ipStr == metaIP {
            return true
        }
    }
    return false
}
```

#### Browser Isolation

Each session gets isolated browser profile:

```go
func (b *BrowserManager) startBrowser(sessionID uuid.UUID) error {
    // Create isolated profile directory
    profileDir := filepath.Join("/tmp/browser-profiles", sessionID.String())
    os.MkdirAll(profileDir, 0700)
    
    // Chromium security flags
    flags := []string{
        "--user-data-dir=" + profileDir,
        "--no-first-run",
        "--no-default-browser-check",
        "--disable-extensions",
        "--disable-plugins",
        "--disable-sync",
        "--disable-background-networking",
        "--disable-dev-shm-usage",  // Prevent /dev/shm issues in Docker
        "--disable-gpu",
        "--no-sandbox",  // Already in Docker sandbox
        "--incognito",
        
        // JavaScript limits
        "--js-flags=--max-old-space-size=512",  // 512MB JS heap
        
        // Disable dangerous features
        "--disable-web-security",  // Only if explicitly needed
        "--disable-features=TranslateUI,MediaRouter",
    }
    
    return b.launchBrowser(sessionID, flags)
}
```

#### Resource Limits

Prevent resource exhaustion:

```go
type BrowserLimits struct {
    MaxMemoryMB        int
    MaxNavigationTime  time.Duration
    MaxPageLoadTime    time.Duration
    MaxResourcesLoaded int
}

func (b *BrowserManager) monitorResources(sessionID uuid.UUID) {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    for range ticker.C {
        // Get browser memory usage
        mem := b.getBrowserMemory(sessionID)
        
        if mem > b.limits.MaxMemoryMB * 1024 * 1024 {
            log.Warn("Browser memory limit exceeded, killing",
                "session", sessionID,
                "memory_mb", mem/(1024*1024),
            )
            b.killBrowser(sessionID)
            return
        }
    }
}

func (b *BrowserManager) Navigate(sessionID uuid.UUID, url string) error {
    // Set timeout
    ctx, cancel := context.WithTimeout(context.Background(), b.limits.MaxNavigationTime)
    defer cancel()
    
    // Navigate with timeout
    done := make(chan error)
    go func() {
        done <- b.performNavigation(sessionID, url)
    }()
    
    select {
    case err := <-done:
        return err
    case <-ctx.Done():
        return errors.New("navigation timeout exceeded")
    }
}
```

### Network Security

- Browser sessions require `network_enabled: true`
- Use network policies to allowlist specific domains only
- Block ads/trackers by default (saves bandwidth)
- **Always** block private IPs and cloud metadata endpoints

### Resource Limits

- Chromium can use 500MB+ RAM
- Set higher memory limits for browser sessions:
  ```json
  {
    "image": "vaultrun/browser:playwright-python",
    "memory_limit_mb": 1024,
    "cpu_limit": 2.0
  }
  ```

### XSS Protection

- Browser runs in isolated sandbox
- No access to host system
- Agents can't execute arbitrary code on agent's machine (only in VaultRun sandbox)

---

## Performance Optimization

### Image Size

- Base Chromium image: ~500MB
- With Playwright: ~600MB
- Optimization: Use `chromium-headless-shell` (smaller binary)

### Startup Time

- Cold start: ~3-5 seconds (browser launch)
- Optimization: Keep browser process alive between tool calls
- Future: Warm browser pool (similar to warm container pool)

---

## Artifacts & Storage

### Auto-captured Artifacts

Every browser session automatically saves:
- **Screenshots** — on demand via `browser_screenshot`
- **Console logs** — browser console output
- **Network HAR** — HTTP archive (optional)
- **Traces** — Playwright trace files for debugging (optional)

### Storage

```bash
# Local storage
ARTIFACT_STORAGE=local
ARTIFACT_LOCAL_PATH=/data/artifacts

# S3 storage (recommended for prod)
ARTIFACT_STORAGE=s3
S3_ARTIFACT_BUCKET=vaultrun-artifacts
```

---

## Testing Strategy

### Unit Tests

- [ ] Mock Docker exec for browser operations
- [ ] Test screenshot artifact creation
- [ ] Test error handling (timeout, selector not found)

### Integration Tests

- [ ] Real browser in Docker container
- [ ] Navigate to test page
- [ ] Click/fill/extract operations
- [ ] Screenshot and PDF generation

### E2E Tests

- [ ] Full MCP tool flow
- [ ] Dashboard artifact display
- [ ] Large page performance (10MB+ HTML)

---

## Documentation

### New Pages

- [ ] `docs/browser-automation.md` — Complete guide
- [ ] `examples/browser-automation/` — Example scripts

### Updates

- [ ] README.md — Add browser use case
- [ ] MCP README — Document 8 new tools
- [ ] CHANGELOG.md — Add feature

---

## Future Enhancements

### Phase 2 Features

1. **Video recording** — Record full session as MP4
2. **Network interception** — Block/mock requests
3. **Geolocation/timezone spoofing** — For location-specific testing
4. **Mobile device emulation** — iPhone, Android viewports
5. **Cookie/localStorage management** — Persist session state
6. **Multi-browser support** — Firefox, Safari (via WebKit)
7. **Browser DevTools Protocol** — Direct CDP access for advanced use cases

---

## Success Metrics

- % of sessions using browser images (target: 20%)
- Browser tool calls per week
- Avg artifacts per browser session (screenshots, PDFs)
- User feedback on browser automation ease-of-use

---

## References

- Playwright: https://playwright.dev/
- Puppeteer: https://pptr.dev/
- Browserless (inspiration): https://browserless.io/
- Existing artifact storage: `internal/storage/`
