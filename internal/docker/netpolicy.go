package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
)

// ErrEgressUnavailable means a session asked for allowed_hosts but this
// deployment has no way to install host iptables rules.
var ErrEgressUnavailable = errors.New("network allowlist (allowed_hosts) is not available in this deployment")

var (
	netpolicyMu     sync.RWMutex
	netpolicyClient *Client
	netpolicyCtr    string
)

// UseNetpolicyContainer routes iptables calls through `docker exec` into a
// long-running helper container that shares the host network namespace and has
// NET_ADMIN (the "netpolicy" service in deployments/docker-compose.yml).
//
// This is required when the API itself runs in a container: iptables executed
// in the API's own network namespace would never see the sandbox bridge
// traffic, so rules would silently enforce nothing.
func (c *Client) UseNetpolicyContainer(name string) {
	netpolicyMu.Lock()
	defer netpolicyMu.Unlock()
	netpolicyClient, netpolicyCtr = c, name
}

func netpolicyHelper() (*Client, string) {
	netpolicyMu.RLock()
	defer netpolicyMu.RUnlock()
	return netpolicyClient, netpolicyCtr
}

// EgressPolicyAvailable reports whether allowed_hosts can be enforced: either
// iptables is on the API host's PATH (bare-metal install) or a netpolicy
// helper container is configured.
func EgressPolicyAvailable() bool {
	if c, name := netpolicyHelper(); c != nil && name != "" {
		return true
	}
	_, err := exec.LookPath("iptables")
	return err == nil
}

// runInHelper executes bin+args inside the netpolicy container.
func runInHelper(c *Client, name, bin string, args []string) error {
	_, err := runInHelperOutput(c, name, bin, args)
	return err
}

func runInHelperOutput(c *Client, name, bin string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	created, err := c.inner.ContainerExecCreate(ctx, name, types.ExecConfig{
		Cmd:          append([]string{bin}, args...),
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("netpolicy helper %q: %w", name, err)
	}
	resp, err := c.inner.ContainerExecAttach(ctx, created.ID, types.ExecStartCheck{})
	if err != nil {
		return "", fmt.Errorf("netpolicy helper attach: %w", err)
	}
	defer resp.Close()
	var out bytes.Buffer
	if err := demuxDockerStream(resp.Reader, &out, &out); err != nil {
		return "", fmt.Errorf("netpolicy helper output: %w", err)
	}
	info, err := c.inner.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return "", fmt.Errorf("netpolicy helper inspect: %w", err)
	}
	if info.ExitCode != 0 {
		return "", fmt.Errorf("exit status %d: %s", info.ExitCode, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func runIptables(bin string, args ...string) error {
	if c, name := netpolicyHelper(); c != nil && name != "" {
		return runInHelper(c, name, bin, args)
	}
	out, err := exec.Command(bin, args...).CombinedOutput() //nolint:gosec
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

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

// inputChain names the chain for traffic from a sandbox to the host itself.
func inputChain(chain string) string { return chain + "-in" }

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
//  2. ACCEPT DNS (UDP + TCP :53) to the bridge gateway, 127.0.0.11 and the host's upstream resolvers
//  3. ACCEPT for each IP in allowedIPs
//  4. DROP everything else
//
// IPv6 rules: a blanket DROP from the bridge interface is inserted into the
// FORWARD chain to prevent NAT64/DNS-over-IPv6 exfiltration paths. If
// ip6tables is not installed a warning is logged but the error is not fatal
// (the host may have IPv6 disabled at the kernel level).
//
// dnsServer must be the IPv4 gateway of the session network. Port 53 is also
// allowed to Docker's embedded resolver (127.0.0.11) and to the upstream
// resolvers Docker forwards to (the host's nameservers). gVisor with
// --network=host sends the query to 127.0.0.11, and Docker then forwards it
// to those upstreams, which are almost always private addresses. Without
// that allow, the private-range drop below answers the sandbox with
// SERVFAIL. DNS to any other address stays blocked.
//
// openInternet selects the policy for a sandbox with network but no
// allowed_hosts: instead of allowing only allowedIPs it drops every private,
// loopback, link-local (cloud metadata) and CGNAT range and allows the rest,
// so the sandbox reaches the internet but not the network the host sits in.
//
// In both modes traffic addressed to the host itself (the bridge gateway or
// any other host address) is dropped too: it passes the INPUT chain, not
// FORWARD, so without inputChain(chain) a sandbox could reach any service
// the host runs on 0.0.0.0.
func applyEgressPolicy(chain, iface, dnsServer string, allowedIPs []string, openInternet bool) error {
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
	// DNS to the bridge gateway, Docker's stub, and the host's upstream
	// resolvers. These accepts sit above the private-range drop.
	dnsTargets := dnsAllowList(dnsServer)
	if err := addDNSAllows(chain, dnsTargets); err != nil {
		return err
	}
	if openInternet {
		for _, n := range blockedNets {
			if n.IP.To4() == nil {
				continue // IPv6 is dropped wholesale below
			}
			if err := ipt("-A", chain, "-d", n.String(), "-j", "DROP"); err != nil {
				return fmt.Errorf("add private-range drop rule for %s: %w", n, err)
			}
		}
		// The accept below is every other address. Without this drop a
		// sandbox can query 8.8.8.8 or 1.1.1.1 on port 53 directly. The
		// allows above already matched the resolvers we do permit.
		for _, proto := range []string{"udp", "tcp"} {
			if err := ipt("-A", chain, "-p", proto, "--dport", "53", "-j", "DROP"); err != nil {
				return fmt.Errorf("add DNS drop rule: %w", err)
			}
		}
		allowedIPs = []string{"0.0.0.0/0"}
	}
	for _, ip := range allowedIPs {
		// Hosts often resolve to AAAA records too. IPv6 egress is dropped
		// wholesale below, and iptables rejects IPv6 literals.
		if _, n, err := net.ParseCIDR(ip); err == nil {
			if n.IP.To4() == nil {
				continue
			}
		} else if p := net.ParseIP(ip); p == nil || p.To4() == nil {
			continue
		}
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

	// Traffic to the host itself: only DNS, to the same resolvers as above.
	in := inputChain(chain)
	if err := ipt("-N", in); err != nil && !isIPTExistsErr(err) {
		return fmt.Errorf("create chain %q: %w", in, err)
	}
	if err := ipt("-F", in); err != nil {
		return fmt.Errorf("flush chain %q: %w", in, err)
	}
	if err := ipt("-A", in, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("add host-input rule: %w", err)
	}
	if err := addDNSAllows(in, dnsTargets); err != nil {
		return err
	}
	if err := ipt("-A", in, "-j", "DROP"); err != nil {
		return fmt.Errorf("add host-input rule: %w", err)
	}
	if err := ipt("-I", "INPUT", "-i", iface, "-j", in); err != nil && !isIPTExistsErr(err) {
		return fmt.Errorf("insert INPUT jump (iface=%s, chain=%s): %w", iface, in, err)
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
		"chain", chain, "iface", iface, "dns_server", dnsServer, "dns_targets", dnsTargets, "allowed_ips", len(allowedIPs))
	return nil
}

// dockerEmbeddedDNS is the address Docker puts in a container's resolv.conf.
// With gVisor's host network stack the query leaves the sandbox for this
// address and is then forwarded to the daemon's upstream resolvers.
const dockerEmbeddedDNS = "127.0.0.11"

// dnsAllowList is the only addresses a sandbox may send port 53 to: the
// session bridge gateway, Docker's embedded resolver, and the host's
// upstream nameservers.
func dnsAllowList(gateway string) []string {
	out := []string{gateway, dockerEmbeddedDNS}
	out = append(out, hostResolvers()...)
	return dedupIPv4(out)
}

func addDNSAllows(chain string, targets []string) error {
	for _, ip := range targets {
		if err := ipt("-A", chain, "-p", "udp", "--dport", "53", "-d", ip, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add DNS/UDP rule for %s: %w", ip, err)
		}
		if err := ipt("-A", chain, "-p", "tcp", "--dport", "53", "-d", ip, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("add DNS/TCP rule for %s: %w", ip, err)
		}
	}
	return nil
}

// hostResolvers reads the nameservers Docker forwards to. The netpolicy
// helper shares the host network namespace, so its resolv.conf is the
// host's; a bare-metal API reads the file directly. Loopback stubs are
// skipped (Docker does too) and the systemd-resolved upstream file is used
// when the stub is the only nameserver.
func hostResolvers() []string {
	text, err := readResolv("/etc/resolv.conf")
	if err != nil {
		slog.Warn("netpolicy: could not read /etc/resolv.conf; DNS to Docker's upstream resolvers will not be allowed", "err", err)
	}
	got := upstreamResolvers(parseResolvNameservers(text))
	if len(got) > 0 {
		return got
	}
	text, err = readResolv("/run/systemd/resolve/resolv.conf")
	if err != nil {
		slog.Warn("netpolicy: could not read systemd resolver config; DNS to Docker's upstream resolvers will not be allowed", "err", err)
	}
	return upstreamResolvers(parseResolvNameservers(text))
}

func readResolv(path string) (string, error) {
	if c, name := netpolicyHelper(); c != nil && name != "" {
		out, err := runInHelperOutput(c, name, "cat", []string{path})
		if err != nil {
			return "", err
		}
		return out, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseResolvNameservers(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "nameserver") {
			continue
		}
		out = append(out, fields[1])
	}
	return out
}

// upstreamResolvers keeps public and private IPv4 nameservers and drops
// loopback. Docker ignores a 127.0.0.0/8 stub and forwards to the rest;
// those private upstreams are exactly what the egress drop would block.
func upstreamResolvers(servers []string) []string {
	var out []string
	for _, s := range servers {
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() {
			continue
		}
		out = append(out, ip.String())
	}
	return dedupIPv4(out)
}

func dedupIPv4(ips []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			continue
		}
		s = ip.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// removeEgressPolicy removes the iptables/ip6tables rules created by applyEgressPolicy.
// Errors are logged as warnings — cleanup must not block container removal.
func removeEgressPolicy(chain, iface string) {
	in := inputChain(chain)
	if err := ipt("-D", "INPUT", "-i", iface, "-j", in); err != nil {
		slog.Warn("netpolicy: remove INPUT jump", "chain", in, "iface", iface, "err", err)
	}
	if err := ipt("-F", in); err != nil {
		slog.Warn("netpolicy: flush chain", "chain", in, "err", err)
	}
	if err := ipt("-X", in); err != nil {
		slog.Warn("netpolicy: delete chain", "chain", in, "err", err)
	}
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
func ipt(args ...string) error { return runIptables("iptables", args...) }

// ipt6 runs ip6tables with the supplied arguments.
func ipt6(args ...string) error { return runIptables("ip6tables", args...) }

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
