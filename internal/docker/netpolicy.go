package docker

import (
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"regexp"
	"strings"
)

// iptablesNameRe matches valid iptables chain names and Linux interface names:
// only alphanumeric characters, hyphens, and underscores.
var iptablesNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// chainName returns the iptables chain name for a container.
// Uses the first 12 hex chars of the container ID (matching the convention
// Docker uses for network interface naming), prefixed with "vr-".
// Maximum iptables chain name length is 28 characters; this produces 15.
func chainName(containerID string) string {
	const prefix = "vr-"
	if len(containerID) > 12 {
		return prefix + containerID[:12]
	}
	return prefix + containerID
}

// bridgeIface returns the Linux network interface name for a Docker bridge
// network. Docker names custom bridge networks "br-<networkID[:12]>".
func bridgeIface(networkID string) string {
	if len(networkID) > 12 {
		return "br-" + networkID[:12]
	}
	return "br-" + networkID
}

// applyEgressPolicy installs iptables (IPv4) and ip6tables (IPv6) rules that
// enforce an allowlist-based egress policy for a container on the given bridge.
//
// IPv4 rules (custom chain per container):
//  1. ACCEPT ESTABLISHED,RELATED
//  2. ACCEPT DNS (UDP + TCP :53) to dnsServer only — the bridge gateway IP
//  3. ACCEPT for each IP in allowedIPs
//  4. DROP everything else
//
// IPv6 rules: a blanket DROP from the bridge interface is inserted into the
// FORWARD chain to prevent NAT64/DNS-over-IPv6 exfiltration paths. If
// ip6tables is not installed a warning is logged but the error is not fatal
// (the host may have IPv6 disabled at the kernel level).
//
// dnsServer must be the IPv4 gateway of the session network. Restricting DNS
// to this address prevents a container from tunnelling data to an
// attacker-controlled nameserver via arbitrary port-53 queries.
func applyEgressPolicy(chain, iface, dnsServer string, allowedIPs []string) error {
	if !iptablesNameRe.MatchString(chain) {
		return fmt.Errorf("invalid iptables chain name: %q", chain)
	}
	if !iptablesNameRe.MatchString(iface) {
		return fmt.Errorf("invalid network interface name: %q", iface)
	}
	if net.ParseIP(dnsServer) == nil {
		return fmt.Errorf("invalid DNS server IP: %q", dnsServer)
	}

	// ── IPv4 ────────────────────────────────────────────────────────────────

	if err := ipt("-N", chain); err != nil && !isIPTExistsErr(err) {
		return fmt.Errorf("create chain %q: %w", chain, err)
	}
	if err := ipt("-F", chain); err != nil {
		return fmt.Errorf("flush chain %q: %w", chain, err)
	}
	if err := ipt("-A", chain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("add conntrack rule: %w", err)
	}
	// DNS restricted to bridge gateway only.
	if err := ipt("-A", chain, "-p", "udp", "--dport", "53", "-d", dnsServer, "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("add DNS/UDP rule: %w", err)
	}
	if err := ipt("-A", chain, "-p", "tcp", "--dport", "53", "-d", dnsServer, "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("add DNS/TCP rule: %w", err)
	}
	for _, ip := range allowedIPs {
		if err := ipt("-A", chain, "-d", ip, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add allow rule for %q: %w", ip, err)
		}
	}
	if err := ipt("-A", chain, "-j", "DROP"); err != nil {
		return fmt.Errorf("add drop rule: %w", err)
	}
	if err := ipt("-I", "FORWARD", "-i", iface, "-j", chain); err != nil && !isIPTExistsErr(err) {
		return fmt.Errorf("insert FORWARD jump (iface=%s, chain=%s): %w", iface, chain, err)
	}

	// ── IPv6 (best-effort: binary may not be present) ────────────────────────
	//
	// Block all IPv6 egress from the container bridge. Without this rule a
	// container could exfiltrate data by sending DNS queries to an external
	// IPv6 nameserver (NAT64 / DNS-over-IPv6), bypassing the IPv4-only
	// allowlist above. We use a blanket DROP on the interface rather than a
	// full per-container chain because the allowedIPs list contains only IPv4
	// addresses; there are no legitimate IPv6 destinations to permit.
	if err := ipt6("-I", "FORWARD", "-i", iface, "-j", "DROP"); err != nil {
		if isIPT6NotFound(err) {
			slog.Warn("netpolicy: ip6tables not found — IPv6 egress not blocked; ensure IPv6 is disabled on the host",
				"iface", iface)
		} else if !isIPTExistsErr(err) {
			return fmt.Errorf("insert IPv6 DROP (iface=%s): %w", iface, err)
		}
	}

	slog.Info("netpolicy: egress policy applied",
		"chain", chain, "iface", iface, "dns_server", dnsServer, "allowed_ips", len(allowedIPs))
	return nil
}

// removeEgressPolicy removes the iptables/ip6tables rules created by applyEgressPolicy.
// Errors are logged as warnings — cleanup must not block container removal.
func removeEgressPolicy(chain, iface string) {
	if err := ipt("-D", "FORWARD", "-i", iface, "-j", chain); err != nil {
		slog.Warn("netpolicy: remove FORWARD jump", "chain", chain, "iface", iface, "err", err)
	}
	if err := ipt("-F", chain); err != nil {
		slog.Warn("netpolicy: flush chain", "chain", chain, "err", err)
	}
	if err := ipt("-X", chain); err != nil {
		slog.Warn("netpolicy: delete chain", "chain", chain, "err", err)
	}
	// Best-effort IPv6 cleanup.
	if err := ipt6("-D", "FORWARD", "-i", iface, "-j", "DROP"); err != nil && !isIPT6NotFound(err) {
		slog.Warn("netpolicy: remove IPv6 FORWARD DROP", "iface", iface, "err", err)
	}
	slog.Info("netpolicy: egress policy removed", "chain", chain, "iface", iface)
}

// ipt runs iptables with the supplied arguments.
func ipt(args ...string) error {
	out, err := exec.Command("iptables", args...).CombinedOutput() //nolint:gosec
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ipt6 runs ip6tables with the supplied arguments.
func ipt6(args ...string) error {
	out, err := exec.Command("ip6tables", args...).CombinedOutput() //nolint:gosec
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isIPT6NotFound returns true when the error is due to ip6tables binary being absent.
func isIPT6NotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "executable file not found") ||
		strings.Contains(s, "no such file or directory")
}

// isIPTExistsErr returns true when the iptables error indicates the object
// (chain or rule) already exists, allowing idempotent creation.
func isIPTExistsErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Chain already exists") ||
		strings.Contains(s, "Already exists") ||
		strings.Contains(s, "already exists") ||
		strings.Contains(s, "Duplicate rule")
}
