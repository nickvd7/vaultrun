package docker

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ErrInvalidAllowedHosts marks an allowed_hosts entry that can never be
// honoured (malformed, or pointing at a private/internal address). Callers
// report it as a client error rather than a server failure.
var ErrInvalidAllowedHosts = errors.New("invalid allowed_hosts")

const maxAllowedHosts = 64

var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// blockedNets are ranges a sandbox must never be allowed to reach through
// allowed_hosts: loopback, private, link-local (cloud metadata), CGNAT and
// unspecified addresses.
var blockedNets = mustParseCIDRs(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}

func overlapsBlocked(n *net.IPNet) bool {
	for _, b := range blockedNets {
		if b.Contains(n.IP) || n.Contains(b.IP) {
			return true
		}
	}
	return false
}

func blockedIP(ip net.IP) bool {
	if isPrivateIP(ip) {
		return true
	}
	for _, b := range blockedNets {
		if b.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateAllowedHosts checks the syntax and target of every entry: a
// hostname, an IP address or a CIDR block, none of which may point at a
// private, loopback or link-local address. DNS is not consulted.
func ValidateAllowedHosts(hosts []string) error {
	if len(hosts) > maxAllowedHosts {
		return fmt.Errorf("%w: at most %d entries are allowed", ErrInvalidAllowedHosts, maxAllowedHosts)
	}
	for _, h := range hosts {
		if err := validateAllowedHost(h); err != nil {
			return err
		}
	}
	return nil
}

func validateAllowedHost(h string) error {
	if h == "" || h != strings.TrimSpace(h) {
		return fmt.Errorf("%w: %q must be a hostname, IP address or CIDR block without surrounding whitespace", ErrInvalidAllowedHosts, h)
	}
	if ip := net.ParseIP(h); ip != nil {
		if blockedIP(ip) {
			return fmt.Errorf("%w: %q is a private, loopback or link-local address", ErrInvalidAllowedHosts, h)
		}
		return nil
	}
	if strings.Contains(h, "/") {
		_, n, err := net.ParseCIDR(h)
		if err != nil {
			return fmt.Errorf("%w: %q is not a valid CIDR block", ErrInvalidAllowedHosts, h)
		}
		if overlapsBlocked(n) {
			return fmt.Errorf("%w: %q includes private, loopback or link-local addresses", ErrInvalidAllowedHosts, h)
		}
		return nil
	}
	lower := strings.ToLower(strings.TrimSuffix(h, "."))
	if len(lower) > 253 || !hostnameRe.MatchString(lower) {
		return fmt.Errorf("%w: %q is not a valid hostname, IP address or CIDR block", ErrInvalidAllowedHosts, h)
	}
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		strings.HasSuffix(lower, ".internal") || strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("%w: %q refers to an internal name", ErrInvalidAllowedHosts, h)
	}
	return nil
}
