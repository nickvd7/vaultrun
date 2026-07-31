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

	waitUntil, err := validateWaitUntil(opts.WaitUntil)
	if err != nil {
		return err
	}

	timeout := clampTimeout(opts.Timeout)

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

	containerID, cerr := m.getContainerID(ctx, sessionID)
	if cerr != nil {
		return fmt.Errorf("failed to get container: %w", cerr)
	}

	_, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script})
	if err != nil {
		return fmt.Errorf("navigation failed: %w: %s", err, stderr)
	}

	return nil
}

// Screenshot captures a screenshot
func (m *PlaywrightManager) Screenshot(ctx context.Context, sessionID uuid.UUID, opts ScreenshotOpts) (*ScreenshotResult, error) {
	format, err := validateImageFormat(opts.Format)
	if err != nil {
		return nil, err
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

	if _, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script}); err != nil {
		return nil, fmt.Errorf("screenshot failed: %w: %s", err, stderr)
	}

	// Capture the path this host chose, not the one the container printed. The
	// sandbox is the untrusted side of this boundary: a compromised container
	// that echoed "/etc/shadow" would otherwise have the host copy that file out
	// and store it as a downloadable artifact.
	result, err := m.captureArtifact(ctx, containerID, sessionID, tmpPath, fmt.Sprintf("screenshot.%s", format))
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

	timeout := clampTimeout(opts.Timeout)

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

	format, err := validatePaperFormat(opts.Format)
	if err != nil {
		return nil, err
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

	if _, stderr, err := m.docker.ExecSimple(ctx, containerID, []string{"python3", "-c", script}); err != nil {
		return nil, fmt.Errorf("PDF generation failed: %w: %s", err, stderr)
	}

	// As with screenshots, the host-chosen path is used rather than whatever the
	// container echoed back.
	result, err := m.captureArtifact(ctx, containerID, sessionID, tmpPath, "page.pdf")
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
// blockedHostnames are resolved by cloud providers to their metadata service.
// They are rejected by name as well as by IP: a provider may change the address
// or answer only from inside the VPC, where our own resolution would fail open.
var blockedHostnames = map[string]bool{
	"metadata":                       true,
	"metadata.google.internal":       true,
	"metadata.goog":                  true,
	"instance-data":                  true,
	"instance-data.ec2.internal":     true,
}

func (m *PlaywrightManager) validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}

	// Scheme is checked before anything else: file://, javascript:, data: and
	// gopher: must never reach a DNS lookup, let alone the browser.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https URLs are allowed, got %q", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL has no host")
	}

	if m.policy.BlockLocalhost && strings.EqualFold(hostname, "localhost") {
		return fmt.Errorf("navigation to localhost is blocked")
	}

	if m.policy.BlockMetadata && blockedHostnames[strings.ToLower(hostname)] {
		return fmt.Errorf("navigation to cloud metadata endpoint is blocked")
	}

	// When the host is already an IP literal, check it directly. net.LookupIP
	// does handle literals, but doing it here keeps the check working even if
	// the resolver is unavailable.
	if literal := net.ParseIP(hostname); literal != nil {
		return m.checkIP(literal)
	}

	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("hostname %q resolved to no addresses", hostname)
	}

	// Every returned address must pass: a hostname with both a public and a
	// private A record would otherwise be usable to reach the private one.
	for _, ip := range ips {
		if err := m.checkIP(ip); err != nil {
			return err
		}
	}

	return nil
}

// checkIP applies the network policy to a single resolved address.
func (m *PlaywrightManager) checkIP(ip net.IP) error {
	if m.policy.BlockMetadata && isMetadataIP(ip) {
		return fmt.Errorf("navigation to cloud metadata endpoint (%s) is blocked", ip)
	}

	if m.policy.BlockLocalhost && ip.IsLoopback() {
		return fmt.Errorf("navigation to localhost (%s) is blocked", ip)
	}

	if m.policy.BlockPrivateIPs && isPrivateIP(ip) {
		return fmt.Errorf("navigation to private IP (%s) is blocked", ip)
	}

	return nil
}

// privateCIDRs lists every range an agent must not reach from a sandbox.
// Parsed once at init: net.ParseCIDR on every request is both wasteful and
// silently ignores parse errors.
var privateCIDRs = func() []*net.IPNet {
	cidrs := []string{
		// RFC1918 private
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		// Loopback — also covered by ip.IsLoopback(), kept for defence in depth
		"127.0.0.0/8",
		// Link-local, includes the cloud metadata endpoints
		"169.254.0.0/16",
		// Carrier-grade NAT (RFC6598) — routable inside many hosting networks
		"100.64.0.0/10",
		// "This host on this network" (RFC1122) — 0.0.0.0 resolves to localhost
		// on several stacks
		"0.0.0.0/8",
		// Shared/reserved ranges that should never be a legitimate target
		"192.0.0.0/24",   // IETF protocol assignments
		"198.18.0.0/15",  // benchmarking
		"240.0.0.0/4",    // reserved
		// IPv6
		"::1/128",   // loopback
		"fc00::/7",  // unique local addresses
		"fe80::/10", // link-local
		"::/128",    // unspecified
	}

	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("browser: invalid private CIDR %q: %v", cidr, err))
		}
		nets = append(nets, ipNet)
	}
	return nets
}()

// metadataCIDRs lists cloud instance-metadata endpoints. Reaching these leaks
// IAM credentials, so they are checked separately from the private ranges to
// produce a clearer error and to survive any future loosening of those ranges.
var metadataCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"169.254.169.254/32", // AWS, GCP, Azure, DigitalOcean, Oracle
		"169.254.170.2/32",   // AWS ECS task metadata
		"fd00:ec2::254/128",  // AWS IPv6
	}

	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("browser: invalid metadata CIDR %q: %v", cidr, err))
		}
		nets = append(nets, ipNet)
	}
	return nets
}()

// isPrivateIP reports whether ip falls in a range that is unreachable for
// sandboxed agents.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}

	for _, ipNet := range privateCIDRs {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// isMetadataIP reports whether ip is a cloud instance-metadata endpoint.
func isMetadataIP(ip net.IP) bool {
	for _, ipNet := range metadataCIDRs {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// Bounds and accepted values for the option fields that end up inside the
// generated Python.
//
// These are allowlists rather than escapes on purpose: each one is an enum in
// the Playwright API, so anything outside the set is a caller error and there is
// no legitimate value that needs quoting. Escaping would also work, but an
// allowlist keeps the generated script free of caller-controlled text entirely.
var (
	validWaitUntil    = []string{"load", "domcontentloaded", "networkidle", "commit"}
	validImageFormats = []string{"png", "jpeg"}
	validPaperFormats = []string{
		"Letter", "Legal", "Tabloid", "Ledger",
		"A0", "A1", "A2", "A3", "A4", "A5", "A6",
	}
)

// MaxBrowserTimeoutMs bounds how long a single browser operation may block a
// worker. Playwright treats 0 as "no timeout", which would pin a container and
// an API goroutine indefinitely.
const MaxBrowserTimeoutMs = 120000

func oneOf(value, fallback string, allowed []string, field string) (string, error) {
	if value == "" {
		return fallback, nil
	}
	for _, a := range allowed {
		if value == a {
			return value, nil
		}
	}
	return "", fmt.Errorf("invalid %s %q: must be one of %s", field, value, strings.Join(allowed, ", "))
}

func validateWaitUntil(v string) (string, error) {
	return oneOf(v, "load", validWaitUntil, "wait_until")
}

func validateImageFormat(v string) (string, error) {
	return oneOf(v, "png", validImageFormats, "format")
}

func validatePaperFormat(v string) (string, error) {
	return oneOf(v, "A4", validPaperFormats, "format")
}

// clampTimeout keeps a caller-supplied timeout inside a usable range.
func clampTimeout(ms int) int {
	if ms <= 0 {
		return 30000
	}
	if ms > MaxBrowserTimeoutMs {
		return MaxBrowserTimeoutMs
	}
	return ms
}

// escapeString renders s safe for interpolation into a single-quoted Python
// string literal. Backslashes must be doubled first, otherwise the escaping of
// the quotes themselves can be undone by a trailing backslash in the input.
// Newlines are escaped because a raw newline terminates the literal and lets
// the caller append arbitrary Python statements.
func escapeString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)

	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0:
			// A NUL byte truncates the string for some C-backed parsers.
			b.WriteString(`\x00`)
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
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
