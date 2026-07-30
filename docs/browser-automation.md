# Browser Automation in VaultRun

VaultRun biedt headless browser automation via Playwright, geïntegreerd in de sandbox omgeving. Gebruik dit voor web scraping, E2E testing, of geautomatiseerde workflows.

## Quick Start

### 1. Docker Image

Gebruik de pre-built browser image met Playwright en Chromium:

```bash
# Pull de browser image
docker pull vaultrun/browser:playwright-python

# Of build lokaal
docker build -f docker/browser-playwright-python.Dockerfile -t vaultrun/browser:playwright-python .
```

### 2. Create Session met Browser Support

```bash
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Authorization: Bearer vr_your_key_here" \
  -H "Content-Type: application/json" \
  -d '{
    "image": "vaultrun/browser:playwright-python",
    "name": "my-browser-session"
  }'
```

### 3. Run Playwright Scripts

```python
# Voorbeeld: scrape Hacker News headlines
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    
    # Navigate
    page.goto('https://news.ycombinator.com')
    
    # Extract headlines
    headlines = page.locator('.storylink').all_text_contents()
    for h in headlines:
        print(h)
    
    # Screenshot
    page.screenshot(path='/workspace/screenshot.png', full_page=True)
    
    browser.close()
```

## Browser Manager API

Voor programmatische toegang gebruik je de `internal/browser` package:

```go
import (
    "context"
    "github.com/nickvd7/vaultrun/internal/browser"
    "github.com/google/uuid"
)

// Initialize browser manager
mgr := browser.New(dockerClient, db, artifactStore)

// Navigate
err := mgr.Navigate(ctx, sessionID, "https://example.com", browser.NavigateOpts{
    WaitUntil: "load",
    Timeout:   30000,
})

// Take screenshot
result, err := mgr.Screenshot(ctx, sessionID, browser.ScreenshotOpts{
    FullPage: true,
    Format:   "png",
})
fmt.Printf("Screenshot saved: %s\n", result.Path)
```

## Security Features

### SSRF Protection

De browser manager blokkeert automatisch:
- Private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
- Localhost (127.0.0.1)
- Cloud metadata endpoints (169.254.169.254)

```go
// Geblokkeerd:
mgr.Navigate(ctx, sessionID, "http://localhost:8080/admin", opts)
mgr.Navigate(ctx, sessionID, "http://192.168.1.1/", opts)
mgr.Navigate(ctx, sessionID, "http://169.254.169.254/latest/meta-data/", opts)

// Toegestaan:
mgr.Navigate(ctx, sessionID, "https://example.com", opts)
```

### Network Policy

Configureer toegestane hosts:

```go
policy := &browser.NetworkPolicy{
    AllowedHosts:    []string{"example.com", "api.github.com"},
    BlockPrivateIPs: true,
    BlockLocalhost:  true,
    BlockMetadata:   true,
}
```

## Use Cases

### Web Scraping

```python
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page()
    
    page.goto('https://example.com/products')
    
    products = []
    for item in page.locator('.product-card').all():
        products.append({
            'name': item.locator('.name').text_content(),
            'price': item.locator('.price').text_content(),
        })
    
    print(products)
    browser.close()
```

### E2E Testing

```python
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page()
    
    # Login flow
    page.goto('https://app.example.com/login')
    page.fill('#email', 'test@example.com')
    page.fill('#password', 'password123')
    page.click('button[type=submit]')
    
    # Verify dashboard loaded
    page.wait_for_selector('#dashboard', timeout=5000)
    assert 'Dashboard' in page.title()
    
    # Screenshot for report
    page.screenshot(path='/workspace/dashboard.png')
    
    browser.close()
```

### PDF Generation

```python
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page()
    
    page.goto('https://company.com/annual-report')
    page.wait_for_selector('.report-loaded')
    
    page.pdf(path='/workspace/report.pdf', format='A4')
    
    browser.close()
```

## Artifacts

Screenshots en PDFs worden automatisch opgeslagen als artifacts:

```bash
# List artifacts
curl http://localhost:8080/api/v1/sessions/{session_id}/artifacts \
  -H "Authorization: Bearer vr_your_key_here"

# Download artifact
curl http://localhost:8080/api/v1/artifacts/{artifact_id}/download \
  -H "Authorization: Bearer vr_your_key_here" \
  -o screenshot.png
```

## Performance Tips

### Reuse Browser Context

```python
# Slecht: browser opnieuw opstarten voor elke pagina
for url in urls:
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        page.goto(url)
        # ...
        browser.close()

# Goed: hergebruik browser instance
with sync_playwright() as p:
    browser = p.chromium.launch()
    for url in urls:
        page = browser.new_page()
        page.goto(url)
        # ...
        page.close()
    browser.close()
```

### Block Resources

```python
# Block images en CSS voor snellere scraping
page.route("**/*", lambda route: (
    route.abort() if route.request.resource_type in ["image", "stylesheet"]
    else route.continue_()
))
```

### Headless Mode

```python
# Headless is sneller (default)
browser = p.chromium.launch(headless=True)

# Headed mode voor debugging
browser = p.chromium.launch(headless=False)
```

## Troubleshooting

### "Failed to launch browser"

Controleer of Playwright geïnstalleerd is:

```bash
python3 -c "from playwright.sync_api import sync_playwright; p = sync_playwright().start(); p.chromium.launch(); p.stop()"
```

### "Navigation timeout"

Verhoog de timeout:

```python
page.goto('https://slow-site.com', timeout=60000)  # 60 seconds
```

### Memory Issues

Beperk het aantal gelijktijdige pages:

```python
MAX_PAGES = 5
semaphore = asyncio.Semaphore(MAX_PAGES)

async def scrape_with_limit(url):
    async with semaphore:
        page = await browser.new_page()
        await page.goto(url)
        # ...
        await page.close()
```

## Limitations

- **Browser support:** Alleen Chromium (geen Firefox of WebKit)
- **Video recording:** Nog niet ondersteund
- **Extensions:** Chrome extensions niet ondersteund
- **WebRTC:** Beperkte WebRTC functionaliteit

## Future Enhancements

- [ ] Node.js/Puppeteer image
- [ ] Video recording van sessions
- [ ] HAR file export (network traffic)
- [ ] Browser DevTools Protocol access
- [ ] MCP tools voor browser automation

## Resources

- [Playwright Documentation](https://playwright.dev/python/)
- [Playwright API Reference](https://playwright.dev/python/docs/api/class-playwright)
- [VaultRun Browser Examples](../examples/browser/)
