package nlpolicy

import (
	"context"
	"time"
)

// Parser converts natural language to structured policy
type Parser interface {
	// Parse converts natural language policy description to structured Policy
	Parse(ctx context.Context, naturalLanguage string) (*Policy, error)

	// Validate checks if a natural language policy is valid and actionable
	Validate(ctx context.Context, naturalLanguage string) (*ValidationResult, error)
}

// Policy represents a structured security policy parsed from natural language
type Policy struct {
	ResourceLimits ResourceLimits `json:"resource_limits"`
	NetworkPolicy  NetworkPolicy  `json:"network_policy"`
	CommandPolicy  CommandPolicy  `json:"command_policy"`
	FilePolicy     FilePolicy     `json:"file_policy"`
	Explanation    string         `json:"explanation"` // Human-readable summary of what the policy does
	Warnings       []string       `json:"warnings"`    // Potential issues or recommendations
	GeneratedAt    time.Time      `json:"generated_at"`
}

// ResourceLimits defines compute resource constraints
type ResourceLimits struct {
	CPULimit       float64 `json:"cpu_limit"`        // CPU cores (e.g. 2.0)
	MemoryLimitMB  int     `json:"memory_limit_mb"`  // Memory in MB (e.g. 4096)
	TimeoutSeconds int     `json:"timeout_seconds"`  // Session timeout (e.g. 1800)
	MaxProcesses   int     `json:"max_processes"`    // Max concurrent processes (0 = unlimited)
}

// NetworkPolicy defines network access rules
type NetworkPolicy struct {
	Enabled      bool     `json:"enabled"`        // Whether network is enabled at all
	AllowedHosts []string `json:"allowed_hosts"`  // Allowlist of domains/IPs (empty = allow all)
	BlockedPorts []int    `json:"blocked_ports"`  // Ports to block (e.g. [25, 587] for SMTP)
	AllowDNS     bool     `json:"allow_dns"`      // Whether to allow DNS lookups
	AllowLoopback bool    `json:"allow_loopback"` // Whether to allow localhost connections
}

// CommandPolicy defines which commands can be executed
type CommandPolicy struct {
	AllowedCommands []string `json:"allowed_commands"` // Commands that are allowed (nil = allow all)
	BlockedCommands []string `json:"blocked_commands"` // Commands that are explicitly blocked
	BlockSudo       bool     `json:"block_sudo"`       // Whether to block sudo/su
	BlockNetwork    bool     `json:"block_network"`    // Whether to block network commands (curl, wget, etc)
}

// FilePolicy defines filesystem access rules
type FilePolicy struct {
	AllowedPaths []string `json:"allowed_paths"` // Paths that can be accessed (nil = allow all in /workspace)
	BlockedPaths []string `json:"blocked_paths"` // Paths that are explicitly blocked
	ReadOnly     bool     `json:"read_only"`     // Whether filesystem is read-only
}

// ValidationResult contains validation feedback
type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`   // Blocking issues
	Warnings []string `json:"warnings"` // Non-blocking recommendations
	Suggestions []string `json:"suggestions"` // Helpful improvements
}

// PolicyTemplate is a pre-defined policy for common use cases
type PolicyTemplate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Template    string `json:"template"` // Natural language template
	Category    string `json:"category"` // e.g. "data-science", "web-dev", "security-audit"
}

// Common policy templates
var PolicyTemplates = []PolicyTemplate{
	{
		Name:        "python-data-science",
		Description: "Python environment for data analysis with network access to PyPI and GitHub",
		Category:    "data-science",
		Template: `Python data science environment.

Network access:
- Allow pypi.org for package installation
- Allow github.com for repository access
- Block all other outbound connections

Resources:
- 2 CPU cores maximum
- 4GB memory maximum
- 30 minute timeout

Commands:
- Allow python, pip, git, jupyter
- Block curl, wget, netcat
- No sudo or privileged operations

Files:
- Full access to /workspace
- No access to /etc, /root, or /proc`,
	},
	{
		Name:        "nodejs-web-app",
		Description: "Node.js environment for web application development",
		Category:    "web-dev",
		Template: `Node.js web application environment.

Network access:
- Allow registry.npmjs.org for packages
- Allow github.com for repositories
- Allow port 3000-9000 for development servers
- Block all other outbound connections

Resources:
- 2 CPU cores maximum
- 2GB memory maximum
- 60 minute timeout

Commands:
- Allow node, npm, npx, git
- Block curl, wget (use npm for dependencies)
- No sudo or privileged operations

Files:
- Full access to /workspace
- No access to /etc, /root`,
	},
	{
		Name:        "security-audit",
		Description: "Restricted environment for security testing and auditing",
		Category:    "security",
		Template: `Security audit environment with strict isolation.

Network access:
- Completely disabled (air-gapped)

Resources:
- 1 CPU core maximum
- 1GB memory maximum
- 15 minute timeout

Commands:
- Allow only: ls, cat, grep, find, sha256sum
- Block all network tools
- Block all compilers and interpreters
- No sudo or privileged operations

Files:
- Read-only access to /workspace
- No write operations
- No access to /etc, /root, /proc`,
	},
	{
		Name:        "unrestricted-dev",
		Description: "Minimal restrictions for trusted development workflows",
		Category:    "development",
		Template: `Development environment with minimal restrictions.

Network access:
- Enabled for all domains
- All ports allowed

Resources:
- 4 CPU cores maximum
- 8GB memory maximum
- 120 minute timeout

Commands:
- All commands allowed except sudo
- No privileged operations

Files:
- Full access to /workspace
- No access to /etc, /root, /proc`,
	},
}

// GetTemplate returns a template by name
func GetTemplate(name string) *PolicyTemplate {
	for _, t := range PolicyTemplates {
		if t.Name == name {
			return &t
		}
	}
	return nil
}

// ListTemplates returns all available templates, optionally filtered by category
func ListTemplates(category string) []PolicyTemplate {
	if category == "" {
		return PolicyTemplates
	}
	
	var filtered []PolicyTemplate
	for _, t := range PolicyTemplates {
		if t.Category == category {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
