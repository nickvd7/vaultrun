package nlpolicy

import (
	"fmt"
	"strings"
)

// Compiler converts structured Policy to executable formats
type Compiler struct{}

// NewCompiler creates a new policy compiler
func NewCompiler() *Compiler {
	return &Compiler{}
}

// CompileToOPA converts Policy to OPA Rego policy
func (c *Compiler) CompileToOPA(policy *Policy) (string, error) {
	var rego strings.Builder

	rego.WriteString("package vaultrun\n\n")
	rego.WriteString("# Auto-generated policy from natural language\n")
	rego.WriteString(fmt.Sprintf("# %s\n\n", policy.Explanation))

	rego.WriteString("default allow = false\n\n")

	// Command policy
	if len(policy.CommandPolicy.AllowedCommands) > 0 {
		rego.WriteString("# Allowed commands\n")
		rego.WriteString("allowed_commands = {\n")
		for _, cmd := range policy.CommandPolicy.AllowedCommands {
			rego.WriteString(fmt.Sprintf("  %q,\n", cmd))
		}
		rego.WriteString("}\n\n")

		rego.WriteString("allow {\n")
		rego.WriteString("  input.command\n")
		rego.WriteString("  allowed_commands[input.command]\n")
		rego.WriteString("}\n\n")
	}

	// Blocked commands
	if len(policy.CommandPolicy.BlockedCommands) > 0 {
		rego.WriteString("# Blocked commands\n")
		rego.WriteString("blocked_commands = {\n")
		for _, cmd := range policy.CommandPolicy.BlockedCommands {
			rego.WriteString(fmt.Sprintf("  %q,\n", cmd))
		}
		rego.WriteString("}\n\n")

		rego.WriteString("deny[msg] {\n")
		rego.WriteString("  blocked_commands[input.command]\n")
		rego.WriteString(fmt.Sprintf("  msg := sprintf(\"Command %%v is blocked\", [input.command])\n"))
		rego.WriteString("}\n\n")
	}

	// Sudo blocking
	if policy.CommandPolicy.BlockSudo {
		rego.WriteString("# Block sudo/su\n")
		rego.WriteString("deny[msg] {\n")
		rego.WriteString("  input.command\n")
		rego.WriteString("  regex.match(`^(sudo|su)$`, input.command)\n")
		rego.WriteString("  msg := \"Privileged commands (sudo/su) are blocked\"\n")
		rego.WriteString("}\n\n")
	}

	// File policy
	if len(policy.FilePolicy.BlockedPaths) > 0 {
		rego.WriteString("# Blocked file paths\n")
		rego.WriteString("blocked_paths = {\n")
		for _, path := range policy.FilePolicy.BlockedPaths {
			rego.WriteString(fmt.Sprintf("  %q,\n", path))
		}
		rego.WriteString("}\n\n")

		rego.WriteString("deny[msg] {\n")
		rego.WriteString("  input.path\n")
		rego.WriteString("  some blocked_path\n")
		rego.WriteString("  blocked_paths[blocked_path]\n")
		rego.WriteString("  startswith(input.path, blocked_path)\n")
		rego.WriteString(fmt.Sprintf("  msg := sprintf(\"Access to path %%v is blocked\", [input.path])\n"))
		rego.WriteString("}\n\n")
	}

	// Default allow if no specific rules
	if len(policy.CommandPolicy.AllowedCommands) == 0 && len(policy.CommandPolicy.BlockedCommands) == 0 {
		rego.WriteString("# Allow all commands (no restrictions)\n")
		rego.WriteString("allow {\n")
		rego.WriteString("  true\n")
		rego.WriteString("}\n")
	}

	return rego.String(), nil
}

// CompileToDockerConfig converts Policy to Docker container constraints
func (c *Compiler) CompileToDockerConfig(policy *Policy) (*DockerConfig, error) {
	config := &DockerConfig{
		CPULimit:      policy.ResourceLimits.CPULimit,
		MemoryLimitMB: policy.ResourceLimits.MemoryLimitMB,
		NetworkMode:   "none", // Default to isolated
		CapDrop:       []string{"ALL"}, // Drop all capabilities by default
		ReadOnlyRootfs: policy.FilePolicy.ReadOnly,
	}

	// Network mode
	if policy.NetworkPolicy.Enabled {
		config.NetworkMode = "bridge"
	}

	// Add safe capabilities if not too restrictive
	if !policy.FilePolicy.ReadOnly {
		config.CapAdd = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"}
	}

	return config, nil
}

// CompileToNetworkRules converts Policy to iptables/network rules
func (c *Compiler) CompileToNetworkRules(policy *Policy) (*NetworkRules, error) {
	rules := &NetworkRules{
		Enabled:      policy.NetworkPolicy.Enabled,
		AllowedHosts: policy.NetworkPolicy.AllowedHosts,
		BlockedPorts: policy.NetworkPolicy.BlockedPorts,
		AllowDNS:     policy.NetworkPolicy.AllowDNS,
	}

	// Generate iptables commands
	if policy.NetworkPolicy.Enabled {
		rules.IptablesRules = []string{
			"# Generated network rules",
			"iptables -P OUTPUT DROP", // Default drop
		}

		if policy.NetworkPolicy.AllowDNS {
			rules.IptablesRules = append(rules.IptablesRules,
				"iptables -A OUTPUT -p udp --dport 53 -j ACCEPT", // Allow DNS
			)
		}

		if policy.NetworkPolicy.AllowLoopback {
			rules.IptablesRules = append(rules.IptablesRules,
				"iptables -A OUTPUT -o lo -j ACCEPT", // Allow loopback
			)
		}

		// Allow specific hosts
		for _, host := range policy.NetworkPolicy.AllowedHosts {
			rules.IptablesRules = append(rules.IptablesRules,
				fmt.Sprintf("# Allow %s", host),
				fmt.Sprintf("iptables -A OUTPUT -d %s -j ACCEPT", host),
			)
		}

		// Block specific ports
		for _, port := range policy.NetworkPolicy.BlockedPorts {
			rules.IptablesRules = append(rules.IptablesRules,
				fmt.Sprintf("iptables -A OUTPUT -p tcp --dport %d -j DROP", port),
			)
		}
	} else {
		rules.IptablesRules = []string{
			"# Network disabled",
			"iptables -P OUTPUT DROP",
			"iptables -P INPUT DROP",
		}
	}

	return rules, nil
}

// DockerConfig represents Docker container configuration
type DockerConfig struct {
	CPULimit       float64  `json:"cpu_limit"`
	MemoryLimitMB  int      `json:"memory_limit_mb"`
	NetworkMode    string   `json:"network_mode"` // "none", "bridge"
	CapAdd         []string `json:"cap_add"`      // Capabilities to add
	CapDrop        []string `json:"cap_drop"`     // Capabilities to drop
	ReadOnlyRootfs bool     `json:"read_only_rootfs"`
}

// NetworkRules represents network access rules
type NetworkRules struct {
	Enabled       bool     `json:"enabled"`
	AllowedHosts  []string `json:"allowed_hosts"`
	BlockedPorts  []int    `json:"blocked_ports"`
	AllowDNS      bool     `json:"allow_dns"`
	IptablesRules []string `json:"iptables_rules"` // Generated iptables commands
}

// CompileAll compiles policy to all formats
func (c *Compiler) CompileAll(policy *Policy) (*CompiledPolicy, error) {
	opa, err := c.CompileToOPA(policy)
	if err != nil {
		return nil, fmt.Errorf("OPA compilation failed: %w", err)
	}

	docker, err := c.CompileToDockerConfig(policy)
	if err != nil {
		return nil, fmt.Errorf("Docker config compilation failed: %w", err)
	}

	network, err := c.CompileToNetworkRules(policy)
	if err != nil {
		return nil, fmt.Errorf("Network rules compilation failed: %w", err)
	}

	return &CompiledPolicy{
		SourcePolicy:  policy,
		OPAPolicy:     opa,
		DockerConfig:  docker,
		NetworkRules:  network,
	}, nil
}

// CompiledPolicy contains all compiled policy formats
type CompiledPolicy struct {
	SourcePolicy  *Policy       `json:"source_policy"`
	OPAPolicy     string        `json:"opa_policy"`
	DockerConfig  *DockerConfig `json:"docker_config"`
	NetworkRules  *NetworkRules `json:"network_rules"`
}
