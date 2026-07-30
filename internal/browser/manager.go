package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/artifacts"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
)

// PlaywrightManager implements Manager using Playwright via Docker exec
type PlaywrightManager struct {
	docker  *dockerpkg.Client
	db      *sqlx.DB
	storage artifacts.Store
	policy  *NetworkPolicy
}

// New creates a new browser manager
func New(docker *dockerpkg.Client, db *sqlx.DB, storage artifacts.Store) Manager {
	return &PlaywrightManager{
		docker:  docker,
		db:      db,
		storage: storage,
		policy:  DefaultNetworkPolicy(),
	}
}

// Navigate navigates to a URL
func (m *PlaywrightManager) Navigate(ctx context.Context, sessionID uuid.UUID, targetURL string, opts NavigateOpts) error {
	// Security: validate URL
	if err := m.validateURL(targetURL); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	waitUntil := opts.WaitUntil
	if waitUntil == "" {
		waitUntil = "load"
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30000 // 30 seconds
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        page.set_default_timeout(%d)
        page.goto('%s', wait_until='%s')
        browser.close()
        print("OK")
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, timeout, escapeString(targetURL), waitUntil)

	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get container: %w", err)
	}

	_, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return fmt.Errorf("navigation failed: %w: %s", err, stderr)
	}

	return nil
}

// Screenshot captures a screenshot
func (m *PlaywrightManager) Screenshot(ctx context.Context, sessionID uuid.UUID, opts ScreenshotOpts) (*ScreenshotResult, error) {
	format := opts.Format
	if format == "" {
		format = "png"
	}

	fullPageFlag := "False"
	if opts.FullPage {
		fullPageFlag = "True"
	}

	// Generate temp path in container
	tmpPath := fmt.Sprintf("/tmp/screenshot_%s.%s", uuid.New().String()[:8], format)

	selectorCode := ""
	if opts.Selector != "" {
		selectorCode = fmt.Sprintf(`
    element = page.locator('%s').first
    element.screenshot(path='%s', type='%s')
`, escapeString(opts.Selector), tmpPath, format)
	} else {
		selectorCode = fmt.Sprintf(`
    page.screenshot(path='%s', full_page=%s, type='%s')
`, tmpPath, fullPageFlag, format)
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        %s
        browser.close()
        print("%s")
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, selectorCode, tmpPath)

	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get container: %w", err)
	}

	stdout, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return nil, fmt.Errorf("screenshot failed: %w: %s", err, stderr)
	}

	// Extract screenshot path from stdout
	screenshotPath := strings.TrimSpace(stdout)

	// Copy screenshot from container to host and store as artifact
	result, err := m.captureArtifact(ctx, containerID, sessionID, screenshotPath, fmt.Sprintf("screenshot.%s", format))
	if err != nil {
		return nil, fmt.Errorf("failed to capture screenshot: %w", err)
	}

	return &ScreenshotResult{
		ArtifactID: result.ArtifactID,
		Path:       result.Path,
		SizeBytes:  result.SizeBytes,
		Format:     format,
	}, nil
}

// Click clicks an element
func (m *PlaywrightManager) Click(ctx context.Context, sessionID uuid.UUID, selector string) error {
	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get container: %w", err)
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        page.locator('%s').first.click()
        browser.close()
        print("OK")
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, escapeString(selector))

	_, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return fmt.Errorf("click failed: %w: %s", err, stderr)
	}

	return nil
}

// Fill fills an input field
func (m *PlaywrightManager) Fill(ctx context.Context, sessionID uuid.UUID, selector, value string) error {
	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get container: %w", err)
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        page.locator('%s').first.fill('%s')
        browser.close()
        print("OK")
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, escapeString(selector), escapeString(value))

	_, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return fmt.Errorf("fill failed: %w: %s", err, stderr)
	}

	return nil
}

// Extract extracts content from the page
func (m *PlaywrightManager) Extract(ctx context.Context, sessionID uuid.UUID, opts ExtractOpts) (string, error) {
	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("failed to get container: %w", err)
	}

	extractType := opts.Extract
	if extractType == "" {
		extractType = "text"
	}

	var extractCode string
	if opts.Selector != "" {
		switch extractType {
		case "text":
			extractCode = fmt.Sprintf(`content = page.locator('%s').first.text_content()`, escapeString(opts.Selector))
		case "html":
			extractCode = fmt.Sprintf(`content = page.locator('%s').first.inner_html()`, escapeString(opts.Selector))
		default:
			return "", fmt.Errorf("unsupported extract type: %s", extractType)
		}
	} else {
		switch extractType {
		case "text":
			extractCode = `content = page.content()` // Full HTML for now
		case "html":
			extractCode = `content = page.content()`
		default:
			return "", fmt.Errorf("unsupported extract type: %s", extractType)
		}
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        %s
        browser.close()
        print(content)
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, extractCode)

	stdout, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return "", fmt.Errorf("extract failed: %w: %s", err, stderr)
	}

	return stdout, nil
}

// Evaluate runs JavaScript in the browser
func (m *PlaywrightManager) Evaluate(ctx context.Context, sessionID uuid.UUID, jsScript string) (interface{}, error) {
	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get container: %w", err)
	}

	// Base64 encode the script to avoid escaping issues
	encoded := base64.StdEncoding.EncodeToString([]byte(jsScript))

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys
import base64
import json

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        
        # Decode and execute JavaScript
        js_code = base64.b64decode('%s').decode('utf-8')
        result = page.evaluate(js_code)
        
        browser.close()
        print(json.dumps(result))
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, encoded)

	stdout, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return nil, fmt.Errorf("evaluate failed: %w: %s", err, stderr)
	}

	var result interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	return result, nil
}

// Wait waits for an element or timeout
func (m *PlaywrightManager) Wait(ctx context.Context, sessionID uuid.UUID, opts WaitOpts) error {
	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get container: %w", err)
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30000 // 30 seconds
	}

	var waitCode string
	if opts.Selector != "" {
		waitCode = fmt.Sprintf(`page.wait_for_selector('%s', timeout=%d)`, escapeString(opts.Selector), timeout)
	} else {
		waitCode = fmt.Sprintf(`page.wait_for_timeout(%d)`, timeout)
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        %s
        browser.close()
        print("OK")
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, waitCode)

	_, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return fmt.Errorf("wait failed: %w: %s", err, stderr)
	}

	return nil
}

// PDF generates a PDF of the current page
func (m *PlaywrightManager) PDF(ctx context.Context, sessionID uuid.UUID, opts PDFOpts) (*PDFResult, error) {
	containerID, err := m.getContainerID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get container: %w", err)
	}

	format := opts.Format
	if format == "" {
		format = "A4"
	}

	tmpPath := fmt.Sprintf("/tmp/page_%s.pdf", uuid.New().String()[:8])

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import sys

try:
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        page.pdf(path='%s', format='%s')
        browser.close()
        print("%s")
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
`, tmpPath, format, tmpPath)

	stdout, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return nil, fmt.Errorf("PDF generation failed: %w: %s", err, stderr)
	}

	pdfPath := strings.TrimSpace(stdout)

	// Capture PDF as artifact
	result, err := m.captureArtifact(ctx, containerID, sessionID, pdfPath, "page.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to capture PDF: %w", err)
	}

	return &PDFResult{
		ArtifactID: result.ArtifactID,
		Path:       result.Path,
		SizeBytes:  result.SizeBytes,
	}, nil
}

// Close closes browser resources
func (m *PlaywrightManager) Close(ctx context.Context, sessionID uuid.UUID) error {
	// Playwright is stateless in our implementation (each operation launches a new browser)
	// Nothing to clean up
	return nil
}

// Helper: validate URL and check network policy
func (m *PlaywrightManager) validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https URLs are allowed")
	}

	hostname := parsed.Hostname()

	// Resolve hostname to IP
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}

	// Check each IP against policy
	for _, ip := range ips {
		if m.policy.BlockPrivateIPs && isPrivateIP(ip) {
			return fmt.Errorf("navigation to private IP (%s) is blocked", ip)
		}

		if m.policy.BlockLocalhost && ip.IsLoopback() {
			return fmt.Errorf("navigation to localhost is blocked")
		}

		if m.policy.BlockMetadata && isMetadataIP(ip) {
			return fmt.Errorf("navigation to cloud metadata endpoint is blocked")
		}
	}

	return nil
}

// Helper: check if IP is private
func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	for _, cidr := range privateRanges {
		_, ipNet, _ := net.ParseCIDR(cidr)
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// Helper: check if IP is cloud metadata endpoint
func isMetadataIP(ip net.IP) bool {
	metadata := []string{
		"169.254.169.254/32", // AWS, GCP, Azure
		"fd00:ec2::254/128",  // AWS IPv6
	}

	for _, cidr := range metadata {
		_, ipNet, _ := net.ParseCIDR(cidr)
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// Helper: escape single quotes for Python strings
func escapeString(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

// Helper: get container ID from session ID
func (m *PlaywrightManager) getContainerID(ctx context.Context, sessionID uuid.UUID) (string, error) {
	var containerID string
	err := m.db.GetContext(ctx, &containerID, `SELECT container_id FROM sessions WHERE id = $1`, sessionID)
	if err != nil {
		return "", fmt.Errorf("session not found: %w", err)
	}
	return containerID, nil
}

// Helper: capture artifact from container
func (m *PlaywrightManager) captureArtifact(ctx context.Context, containerID string, sessionID uuid.UUID, containerPath, fileName string) (*ScreenshotResult, error) {
	// Read file from container
	content, err := m.docker.ReadFile(ctx, containerID, containerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file from container: %w", err)
	}

	// Save to artifacts directory
	artifactID := uuid.New()
	artifactDir := filepath.Join("/var/vaultrun/artifacts", sessionID.String())
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create artifact directory: %w", err)
	}

	artifactPath := filepath.Join(artifactDir, fmt.Sprintf("%s_%s", artifactID.String()[:8], fileName))
	if err := os.WriteFile(artifactPath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write artifact: %w", err)
	}

	// Store artifact metadata in database
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO artifacts (id, session_id, filename, size_bytes, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, artifactID, sessionID, fileName, len(content))
	if err != nil {
		return nil, fmt.Errorf("failed to store artifact metadata: %w", err)
	}

	return &ScreenshotResult{
		ArtifactID: artifactID,
		Path:       artifactPath,
		SizeBytes:  int64(len(content)),
	}, nil
}
