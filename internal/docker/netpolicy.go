package docker

import (
	"fmt"
	"log/slog"
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

// applyEgressPolicy installs iptables rules that enforce an allowlist-based
// egress policy for a container attached to the given bridge interface.
//
// Rules created (in the custom chain named by chainName(containerID)):
//  1. ACCEPT ESTABLISHED,RELATED (permit reply traffic)
//  2. ACCEPT DNS (UDP + TCP port 53)
//  3. ACCEPT for each IP in allowedIPs
//  4. DROP everything else (default-deny)
//
// A jump from the FORWARD chain routes traffic from the bridge interface into
// this chain.
//
// Returns an error if iptables is unavailable or a rule fails. Callers should
// treat this as a hard failure when strict enforcement is required.
func applyEgressPolicy(chain, iface string, allowedIPs []string) error {
	// Validate chain and interface names to ensure they consist only of safe
	// characters. Both values are derived from Docker-generated IDs, but an
	// explicit check makes the safety assumption visible and auditable.
	if !iptablesNameRe.MatchString(chain) {
		return fmt.Errorf("invalid iptables chain name: %q", chain)
	}
	if !iptablesNameRe.MatchString(iface) {
		return fmt.Errorf("invalid network interface name: %q", iface)
	}

	// 1. Create the chain; idempotent (ignore "already exists" errors).
	if err := ipt("-N", chain); err != nil && !isIPTExistsErr(err) {
		return fmt.Errorf("create chain %q: %w", chain, err)
	}

	// 2. Flush (clean slate — guard against stale rules from a prior crash).
	if err := ipt("-F", chain); err != nil {
		return fmt.Errorf("flush chain %q: %w", chain, err)
	}

	// 3. ESTABLISHED/RELATED — response packets for outbound connections.
	if err := ipt("-A", chain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("add conntrack rule: %w", err)
	}

	// 4. DNS — containers need name resolution to reach allowlisted hosts.
	if err := ipt("-A", chain, "-p", "udp", "--dport", "53", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("add DNS/UDP rule: %w", err)
	}
	if err := ipt("-A", chain, "-p", "tcp", "--dport", "53", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("add DNS/TCP rule: %w", err)
	}

	// 5. Allowlisted destination IPs.
	for _, ip := range allowedIPs {
		if err := ipt("-A", chain, "-d", ip, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add allow rule for %q: %w", ip, err)
		}
	}

	// 6. Default-deny: drop everything that didn't match above.
	if err := ipt("-A", chain, "-j", "DROP"); err != nil {
		return fmt.Errorf("add drop rule: %w", err)
	}

	// 7. Jump from FORWARD for outbound traffic on this bridge interface.
	if err := ipt("-I", "FORWARD", "-i", iface, "-j", chain); err != nil {
		if !isIPTExistsErr(err) {
			return fmt.Errorf("insert FORWARD jump (iface=%s, chain=%s): %w", iface, chain, err)
		}
	}

	slog.Info("netpolicy: egress policy applied",
		"chain", chain, "iface", iface, "allowed_ips", len(allowedIPs))
	return nil
}

// removeEgressPolicy removes the iptables rules created by applyEgressPolicy.
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
	slog.Info("netpolicy: egress policy removed", "chain", chain, "iface", iface)
}

// ipt runs iptables with the supplied arguments and returns any combined error
// output as part of the returned error.
func ipt(args ...string) error {
	out, err := exec.Command("iptables", args...).CombinedOutput() //nolint:gosec
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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
