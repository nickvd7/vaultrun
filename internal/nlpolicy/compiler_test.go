package nlpolicy

import (
	"testing"
)

func TestCompileToOPA(t *testing.T) {
	compiler := NewCompiler()

	policy := &Policy{
		ResourceLimits: ResourceLimits{
			CPULimit:       2.0,
			MemoryLimitMB:  4096,
			TimeoutSeconds: 1800,
		},
		NetworkPolicy: NetworkPolicy{
			Enabled:      true,
			AllowedHosts: []string{"github.com"},
		},
		CommandPolicy: CommandPolicy{
			AllowedCommands: []string{"python", "pip"},
			BlockedCommands: []string{"curl", "wget"},
			BlockSudo:       true,
		},
		FilePolicy: FilePolicy{
			AllowedPaths: []string{"/workspace"},
			BlockedPaths: []string{"/etc", "/root"},
		},
		Explanation: "Test policy",
	}

	opa, err := compiler.CompileToOPA(policy)
	if err != nil {
		t.Fatalf("CompileToOPA failed: %v", err)
	}

	if opa == "" {
		t.Fatal("OPA policy is empty")
	}

	// Check for key components
	if !contains(opa, "package vaultrun") {
		t.Error("OPA policy should start with package declaration")
	}

	if !contains(opa, "allowed_commands") {
		t.Error("OPA policy should include allowed_commands")
	}

	if !contains(opa, "blocked_commands") {
		t.Error("OPA policy should include blocked_commands")
	}

	if !contains(opa, "sudo") {
		t.Error("OPA policy should block sudo")
	}
}

func TestCompileToDockerConfig(t *testing.T) {
	compiler := NewCompiler()

	policy := &Policy{
		ResourceLimits: ResourceLimits{
			CPULimit:       2.0,
			MemoryLimitMB:  4096,
		},
		NetworkPolicy: NetworkPolicy{
			Enabled: true,
		},
		FilePolicy: FilePolicy{
			ReadOnly: false,
		},
	}

	config, err := compiler.CompileToDockerConfig(policy)
	if err != nil {
		t.Fatalf("CompileToDockerConfig failed: %v", err)
	}

	if config.CPULimit != 2.0 {
		t.Errorf("CPULimit = %v, want 2.0", config.CPULimit)
	}

	if config.MemoryLimitMB != 4096 {
		t.Errorf("MemoryLimitMB = %v, want 4096", config.MemoryLimitMB)
	}

	if config.NetworkMode != "bridge" {
		t.Errorf("NetworkMode = %v, want bridge", config.NetworkMode)
	}

	if len(config.CapDrop) == 0 {
		t.Error("Should drop capabilities by default")
	}
}

func TestCompileToNetworkRules(t *testing.T) {
	compiler := NewCompiler()

	policy := &Policy{
		NetworkPolicy: NetworkPolicy{
			Enabled:      true,
			AllowedHosts: []string{"github.com", "pypi.org"},
			BlockedPorts: []int{25, 587},
			AllowDNS:     true,
		},
	}

	rules, err := compiler.CompileToNetworkRules(policy)
	if err != nil {
		t.Fatalf("CompileToNetworkRules failed: %v", err)
	}

	if !rules.Enabled {
		t.Error("Rules should be enabled")
	}

	if len(rules.AllowedHosts) != 2 {
		t.Errorf("AllowedHosts count = %v, want 2", len(rules.AllowedHosts))
	}

	if len(rules.IptablesRules) == 0 {
		t.Error("Should generate iptables rules")
	}

	// Check for DNS rule when AllowDNS is true
	foundDNS := false
	for _, rule := range rules.IptablesRules {
		if contains(rule, "53") {
			foundDNS = true
			break
		}
	}
	if !foundDNS {
		t.Error("Should include DNS rule when AllowDNS is true")
	}
}

func TestCompileAll(t *testing.T) {
	compiler := NewCompiler()
	parser := NewMockParser()

	policy, err := parser.Parse(nil, "Test policy")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	compiled, err := compiler.CompileAll(policy)
	if err != nil {
		t.Fatalf("CompileAll failed: %v", err)
	}

	if compiled.SourcePolicy == nil {
		t.Error("SourcePolicy should not be nil")
	}

	if compiled.OPAPolicy == "" {
		t.Error("OPAPolicy should not be empty")
	}

	if compiled.DockerConfig == nil {
		t.Error("DockerConfig should not be nil")
	}

	if compiled.NetworkRules == nil {
		t.Error("NetworkRules should not be nil")
	}
}

func TestDisabledNetwork(t *testing.T) {
	compiler := NewCompiler()

	policy := &Policy{
		NetworkPolicy: NetworkPolicy{
			Enabled: false,
		},
	}

	config, _ := compiler.CompileToDockerConfig(policy)
	if config.NetworkMode != "none" {
		t.Errorf("NetworkMode = %v, want none", config.NetworkMode)
	}

	rules, _ := compiler.CompileToNetworkRules(policy)
	if rules.Enabled {
		t.Error("Network rules should be disabled")
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && 
		(s == substr || len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		containsAt(s, substr)))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
