// Package httputil provides security-hardened HTTP utilities.
package httputil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// NoRedirectClient returns an *http.Client that:
//   - never follows redirects (prevents token/credential leakage to redirect targets)
//   - enforces SSRF protection at DIAL time: every TCP connection's resolved IP
//     is re-checked against the private/internal blocklist, closing the
//     DNS-rebinding / TOCTOU window between URL validation and the actual request
//   - has the supplied timeout (default 15s)
//
// Use for outbound requests where the destination is partially or fully
// operator/user-controlled (callbacks, SIEM exports, secrets backends).
func NoRedirectClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// Control runs after DNS resolution with the concrete address that is
		// about to be dialed. Rejecting private/internal IPs here defeats DNS
		// rebinding: even if validation passed against a public A-record, a
		// later flip to 127.0.0.1 / 169.254.169.254 is caught at connect time.
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("ssrf guard: cannot parse dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("ssrf guard: dial address %q is not an IP", host)
			}
			if isPrivateIP(ip) {
				return fmt.Errorf("ssrf guard: refusing to connect to private/internal address %s", ip)
			}
			return nil
		},
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ValidatePublicURL checks that rawURL:
//  1. Parses successfully
//  2. Uses http or https scheme
//  3. Has a non-empty host
//  4. Does not resolve to a loopback, link-local (169.254.x.x / fe80::/10),
//     or private (RFC 1918 / RFC 4193) address — blocks SSRF to IMDS, Redis,
//     Vault, etc.
//
// Pass requireHTTPS=true to additionally reject plain http:// URLs.
func ValidatePublicURL(rawURL string, requireHTTPS bool) error {
	if rawURL == "" {
		return fmt.Errorf("url is empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if requireHTTPS {
		if u.Scheme != "https" {
			return fmt.Errorf("url must use https")
		}
	} else {
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("url must use http or https scheme")
		}
	}
	hostname := u.Hostname()
	if hostname == "" {
		return fmt.Errorf("url has no host")
	}

	// Resolve the hostname and check every returned IP.
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		// If DNS fails at validation time, reject rather than allow.
		// This prevents bypasses where DNS resolves differently later.
		return fmt.Errorf("cannot resolve host %q: %w", hostname, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("url resolves to a private/internal address (%s) — SSRF protection", addr)
		}
	}
	return nil
}

// isPrivateIP returns true for loopback, link-local, private, and unspecified addresses.
func isPrivateIP(ip net.IP) bool {
	// Normalise IPv4-mapped IPv6 (::ffff:127.0.0.1) to its 4-byte form so the
	// IPv4 ranges below match it; otherwise an attacker could bypass the
	// blocklist with a mapped representation of an internal address.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	privateRanges := []string{
		"127.0.0.0/8",    // loopback
		"::1/128",        // IPv6 loopback
		"169.254.0.0/16", // link-local (AWS IMDS, GCP metadata)
		"fe80::/10",      // IPv6 link-local
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"fc00::/7",       // IPv6 unique local
		"0.0.0.0/8",      // unspecified
		"100.64.0.0/10",  // shared address space (RFC6598)
		"64:ff9b::/96",   // NAT64 — conservatively blocked; can embed internal IPv4 (e.g. IMDS)
		"::/128",         // IPv6 unspecified
	}
	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
