package browser

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

// The checks in this file guard the boundary where caller-supplied text becomes
// either a network destination or Python source. They are exported because the
// MCP server drives the same Playwright scripts over the run-command API and
// must not carry a second, divergent copy of them.

// blockedHostnames are resolved by cloud providers to their metadata service.
// They are rejected by name as well as by IP: a provider may change the address
// or answer only from inside the VPC, where our own resolution would fail open.
var blockedHostnames = map[string]bool{
	"metadata":                   true,
	"metadata.google.internal":   true,
	"metadata.goog":              true,
	"instance-data":              true,
	"instance-data.ec2.internal": true,
}

// ValidateURL reports whether the policy permits navigating to rawURL.
func (p *NetworkPolicy) ValidateURL(rawURL string) error {
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

	if p.BlockLocalhost && strings.EqualFold(hostname, "localhost") {
		return fmt.Errorf("navigation to localhost is blocked")
	}

	if p.BlockMetadata && blockedHostnames[strings.ToLower(hostname)] {
		return fmt.Errorf("navigation to cloud metadata endpoint is blocked")
	}

	// An allowlist, when set, is the whole answer for which hosts are reachable.
	if len(p.AllowedHosts) > 0 && !p.hostAllowed(hostname) {
		return fmt.Errorf("host %q is not in the allowed hosts list", hostname)
	}

	// When the host is already an IP literal, check it directly. net.LookupIP
	// does handle literals, but doing it here keeps the check working even if
	// the resolver is unavailable.
	if literal := net.ParseIP(hostname); literal != nil {
		return p.checkIP(literal)
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
		if err := p.checkIP(ip); err != nil {
			return err
		}
	}

	return nil
}

// hostAllowed matches a hostname against the allowlist. An entry beginning with
// a dot matches that domain and its subdomains; anything else must match whole.
func (p *NetworkPolicy) hostAllowed(hostname string) bool {
	h := strings.ToLower(hostname)
	for _, allowed := range p.AllowedHosts {
		a := strings.ToLower(allowed)
		if strings.HasPrefix(a, ".") {
			if h == a[1:] || strings.HasSuffix(h, a) {
				return true
			}
			continue
		}
		if h == a {
			return true
		}
	}
	return false
}

// checkIP applies the network policy to a single resolved address.
func (p *NetworkPolicy) checkIP(ip net.IP) error {
	if p.BlockMetadata && isMetadataIP(ip) {
		return fmt.Errorf("navigation to cloud metadata endpoint (%s) is blocked", ip)
	}

	if p.BlockLocalhost && ip.IsLoopback() {
		return fmt.Errorf("navigation to localhost (%s) is blocked", ip)
	}

	if p.BlockPrivateIPs && isPrivateIP(ip) {
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
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
		"240.0.0.0/4",   // reserved
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

// DefaultBrowserTimeoutMs is used when the caller supplies no timeout.
const DefaultBrowserTimeoutMs = 30000

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

// ValidateWaitUntil resolves a Playwright load-state option.
func ValidateWaitUntil(v string) (string, error) {
	return oneOf(v, "load", validWaitUntil, "wait_until")
}

// ValidateImageFormat resolves a screenshot image format.
func ValidateImageFormat(v string) (string, error) {
	return oneOf(v, "png", validImageFormats, "format")
}

// ValidatePaperFormat resolves a PDF paper size.
func ValidatePaperFormat(v string) (string, error) {
	return oneOf(v, "A4", validPaperFormats, "format")
}

// ClampTimeout keeps a caller-supplied timeout inside a usable range.
func ClampTimeout(ms int) int {
	if ms <= 0 {
		return DefaultBrowserTimeoutMs
	}
	if ms > MaxBrowserTimeoutMs {
		return MaxBrowserTimeoutMs
	}
	return ms
}

// WorkspaceDir is the only directory inside a sandbox that browser output may be
// written to. It is the mount the API reads artifacts back from, so a path
// outside it produces a file the caller can never retrieve.
const WorkspaceDir = "/workspace"

// ValidateWorkspacePath resolves a caller-supplied output path against the
// workspace and refuses anything that escapes it.
//
// Traversal here does not cross the sandbox boundary — the path is interpreted
// inside the container — but "/workspace/../../etc/hosts" silently writes a file
// the caller cannot read back, and the cleaned path is also what gets
// interpolated into the generated Python. Resolving it up front means the
// generated script only ever names a location that exists for the artifact
// store.
func ValidateWorkspacePath(p, fallback string) (string, error) {
	if p == "" {
		return fallback, nil
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("path must not contain a NUL byte")
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(WorkspaceDir, p)
	}

	clean := path.Clean(p)
	if clean != WorkspaceDir && !strings.HasPrefix(clean, WorkspaceDir+"/") {
		return "", fmt.Errorf("path %q must be under %s", p, WorkspaceDir)
	}
	if clean == WorkspaceDir {
		return "", fmt.Errorf("path %q is a directory, not a file", p)
	}

	return clean, nil
}

// EscapePythonString renders s safe for interpolation into a single-quoted
// Python string literal. Backslashes must be doubled first, otherwise the
// escaping of the quotes themselves can be undone by a trailing backslash in the
// input. Newlines are escaped because a raw newline terminates the literal and
// lets the caller append arbitrary Python statements.
func EscapePythonString(s string) string {
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
