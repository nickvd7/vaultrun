package nlpolicy

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ErrUnsafePolicy is returned when a policy contains a value that cannot be
// safely compiled into Rego, iptables rules or a Docker configuration.
var ErrUnsafePolicy = errors.New("unsafe policy")

// Bounds on the compiled output. A policy is produced by an LLM, so its size is
// not inherently limited; these caps stop a single policy from generating a
// multi-megabyte Rego module or thousands of iptables rules.
const (
	MaxCommandsPerList = 200
	MaxPathsPerList    = 200
	MaxAllowedHosts    = 200
	MaxBlockedPorts    = 100
	MaxExplanationLen  = 2000
	MaxCommandLen      = 200
	MaxPathLen         = 4096
	MaxHostLen         = 253 // RFC 1035 maximum domain name length
)

// commandPattern matches a bare executable name or an absolute path to one.
// Commands are compared against input.command in Rego, so shell syntax has no
// meaning there and its presence indicates a malformed or hostile policy.
var commandPattern = regexp.MustCompile(`^/?[A-Za-z0-9][A-Za-z0-9._\-/+]*$`)

// hostPattern matches a hostname, an IPv4/IPv6 address or a CIDR block. This is
// the important one: allowed hosts are interpolated into
// "iptables -A OUTPUT -d %s -j ACCEPT", so a value containing a space or a
// semicolon could append its own rule and reverse the default-drop policy.
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-:]*[A-Za-z0-9])?(/\d{1,3})?$`)

// Validate checks that a policy can be compiled without injecting syntax into
// any of the three output formats.
//
// A Policy is untrusted input: it is produced by an LLM from a user-supplied
// prompt, so a prompt-injection attack reaches these fields directly. Every
// value that is interpolated into generated code is checked here rather than at
// each interpolation site.
func (p *Policy) Validate() error {
	var problems []string

	if err := validateExplanation(p.Explanation); err != nil {
		problems = append(problems, err.Error())
	}

	if err := validateCommandList("command_policy.allowed_commands", p.CommandPolicy.AllowedCommands); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateCommandList("command_policy.blocked_commands", p.CommandPolicy.BlockedCommands); err != nil {
		problems = append(problems, err.Error())
	}

	if err := validatePathList("file_policy.blocked_paths", p.FilePolicy.BlockedPaths); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validatePathList("file_policy.allowed_paths", p.FilePolicy.AllowedPaths); err != nil {
		problems = append(problems, err.Error())
	}

	if err := validateHostList(p.NetworkPolicy.AllowedHosts); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validatePortList(p.NetworkPolicy.BlockedPorts); err != nil {
		problems = append(problems, err.Error())
	}

	if err := validateResourceLimits(p.ResourceLimits); err != nil {
		problems = append(problems, err.Error())
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsafePolicy, strings.Join(problems, "; "))
	}
	return nil
}

// validateExplanation bounds the human-readable explanation and rejects control
// characters.
//
// The explanation is written into the generated Rego module as a comment. A
// newline in it would end the comment and let the rest of the value be parsed
// as policy code — for example an explanation ending in "\nallow { true }"
// would turn a restrictive policy into an allow-all one.
func validateExplanation(explanation string) error {
	if len(explanation) > MaxExplanationLen {
		return fmt.Errorf("explanation is %d characters, maximum is %d", len(explanation), MaxExplanationLen)
	}

	for _, r := range explanation {
		if r == '\n' || r == '\r' {
			return fmt.Errorf("explanation must not contain newlines (it is emitted as a Rego comment)")
		}
		if r < 0x20 && r != '\t' {
			return fmt.Errorf("explanation must not contain control characters")
		}
	}

	return nil
}

func validateCommandList(field string, commands []string) error {
	if len(commands) > MaxCommandsPerList {
		return fmt.Errorf("%s has %d entries, maximum is %d", field, len(commands), MaxCommandsPerList)
	}

	for _, cmd := range commands {
		if cmd == "" {
			return fmt.Errorf("%s must not contain empty entries", field)
		}
		if len(cmd) > MaxCommandLen {
			return fmt.Errorf("%s entry is %d characters, maximum is %d", field, len(cmd), MaxCommandLen)
		}
		if !commandPattern.MatchString(cmd) {
			return fmt.Errorf("%s entry %q is not a plain command name", field, cmd)
		}
	}

	return nil
}

// validatePathList checks file paths. Paths are emitted as Rego string literals
// via %q, which escapes them correctly, so the constraint here is on control
// characters and length rather than quoting.
func validatePathList(field string, paths []string) error {
	if len(paths) > MaxPathsPerList {
		return fmt.Errorf("%s has %d entries, maximum is %d", field, len(paths), MaxPathsPerList)
	}

	for _, path := range paths {
		if path == "" {
			return fmt.Errorf("%s must not contain empty entries", field)
		}
		if len(path) > MaxPathLen {
			return fmt.Errorf("%s entry is %d characters, maximum is %d", field, len(path), MaxPathLen)
		}
		if strings.ContainsRune(path, 0) {
			return fmt.Errorf("%s entry must not contain a NUL byte", field)
		}
		for _, r := range path {
			if r < 0x20 {
				return fmt.Errorf("%s entry %q must not contain control characters", field, path)
			}
		}
	}

	return nil
}

// validateHostList checks allowed hosts. These are interpolated into an
// iptables command line, so anything that could terminate the argument or the
// command itself must be rejected.
func validateHostList(hosts []string) error {
	if len(hosts) > MaxAllowedHosts {
		return fmt.Errorf("network_policy.allowed_hosts has %d entries, maximum is %d",
			len(hosts), MaxAllowedHosts)
	}

	for _, host := range hosts {
		if host == "" {
			return fmt.Errorf("network_policy.allowed_hosts must not contain empty entries")
		}
		if len(host) > MaxHostLen {
			return fmt.Errorf("network_policy.allowed_hosts entry is %d characters, maximum is %d",
				len(host), MaxHostLen)
		}

		// Rejected explicitly so the error names the problem, rather than
		// falling through to the generic pattern message.
		if strings.ContainsAny(host, " \t\n\r;|&$`<>()'\"\\!*?[]{}#") {
			return fmt.Errorf("network_policy.allowed_hosts entry %q contains shell or iptables metacharacters", host)
		}
		if strings.Contains(host, "://") {
			return fmt.Errorf("network_policy.allowed_hosts entry %q must be a host or CIDR, not a URL", host)
		}
		if strings.Contains(host, "/") && !isCIDR(host) {
			return fmt.Errorf("network_policy.allowed_hosts entry %q must be a host or a valid CIDR block", host)
		}
		if !hostPattern.MatchString(host) {
			return fmt.Errorf("network_policy.allowed_hosts entry %q is not a valid host or CIDR", host)
		}
	}

	return nil
}

func isCIDR(s string) bool {
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

func validatePortList(ports []int) error {
	if len(ports) > MaxBlockedPorts {
		return fmt.Errorf("network_policy.blocked_ports has %d entries, maximum is %d",
			len(ports), MaxBlockedPorts)
	}

	for _, port := range ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("network_policy.blocked_ports entry %d is outside 1-65535", port)
		}
	}

	return nil
}

// validateResourceLimits rejects limits the Docker daemon would refuse or that
// would remove the limit entirely.
func validateResourceLimits(r ResourceLimits) error {
	if r.CPULimit < 0 {
		return fmt.Errorf("resource_limits.cpu_limit must not be negative, got %g", r.CPULimit)
	}
	if r.CPULimit > 64 {
		return fmt.Errorf("resource_limits.cpu_limit is %g, maximum is 64", r.CPULimit)
	}
	if r.MemoryLimitMB < 0 {
		return fmt.Errorf("resource_limits.memory_limit_mb must not be negative, got %d", r.MemoryLimitMB)
	}
	if r.MemoryLimitMB > 262144 {
		return fmt.Errorf("resource_limits.memory_limit_mb is %d, maximum is 262144", r.MemoryLimitMB)
	}
	if r.TimeoutSeconds < 0 {
		return fmt.Errorf("resource_limits.timeout_seconds must not be negative, got %d", r.TimeoutSeconds)
	}
	if r.TimeoutSeconds > 86400 {
		return fmt.Errorf("resource_limits.timeout_seconds is %d, maximum is 86400", r.TimeoutSeconds)
	}

	return nil
}
