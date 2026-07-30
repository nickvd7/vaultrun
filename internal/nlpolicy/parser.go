package nlpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIParser implements Parser using OpenAI's GPT models
type OpenAIParser struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIParser creates a new OpenAI-based policy parser
func NewOpenAIParser(apiKey string) *OpenAIParser {
	return &OpenAIParser{
		apiKey:     apiKey,
		model:      "gpt-4", // or "gpt-4-turbo", "gpt-3.5-turbo"
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Parse converts natural language to structured policy
func (p *OpenAIParser) Parse(ctx context.Context, naturalLanguage string) (*Policy, error) {
	systemPrompt := `You are a security policy expert for VaultRun, a Docker sandbox platform for AI agents.

Convert natural language policy descriptions into structured JSON policies.

Output format (JSON):
{
  "resource_limits": {
    "cpu_limit": 2.0,
    "memory_limit_mb": 4096,
    "timeout_seconds": 1800,
    "max_processes": 100
  },
  "network_policy": {
    "enabled": true,
    "allowed_hosts": ["github.com", "pypi.org"],
    "blocked_ports": [25, 587],
    "allow_dns": true,
    "allow_loopback": false
  },
  "command_policy": {
    "allowed_commands": ["python", "pip", "git"],
    "blocked_commands": ["curl", "wget"],
    "block_sudo": true,
    "block_network": false
  },
  "file_policy": {
    "allowed_paths": ["/workspace"],
    "blocked_paths": ["/etc", "/root", "/proc"],
    "read_only": false
  },
  "explanation": "Python data science environment with network access to PyPI and GitHub only. Max 2 CPU cores, 4GB RAM, 30min timeout.",
  "warnings": ["Blocking curl/wget may prevent some package installations"]
}

Rules:
- Be conservative: default to restrictive policies unless explicitly asked otherwise
- Always block sudo/su unless explicitly mentioned
- Default CPU limit: 1.0, memory: 512MB, timeout: 300s
- Default network: disabled unless explicitly mentioned
- Provide clear explanation and warnings
- blocked_paths should always include /etc, /root, /proc unless explicitly allowed
- allowed_commands: null means "allow all except blocked", [] means "block all"
- allowed_hosts: [] means "allow all", non-empty means allowlist`

	userPrompt := fmt.Sprintf("Convert this policy to JSON:\n\n%s", naturalLanguage)

	reqBody := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature":   0.1, // Low temperature for consistent output
		"response_format": map[string]string{"type": "json_object"},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call OpenAI API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI response")
	}

	content := apiResp.Choices[0].Message.Content

	var policy Policy
	if err := json.Unmarshal([]byte(content), &policy); err != nil {
		return nil, fmt.Errorf("failed to parse policy JSON: %w", err)
	}

	policy.GeneratedAt = time.Now()

	// Post-processing: ensure sensible defaults
	if policy.ResourceLimits.CPULimit == 0 {
		policy.ResourceLimits.CPULimit = 1.0
	}
	if policy.ResourceLimits.MemoryLimitMB == 0 {
		policy.ResourceLimits.MemoryLimitMB = 512
	}
	if policy.ResourceLimits.TimeoutSeconds == 0 {
		policy.ResourceLimits.TimeoutSeconds = 300
	}

	return &policy, nil
}

// Validate checks if a natural language policy is valid
func (p *OpenAIParser) Validate(ctx context.Context, naturalLanguage string) (*ValidationResult, error) {
	// For now, we validate by attempting to parse
	policy, err := p.Parse(ctx, naturalLanguage)
	if err != nil {
		return &ValidationResult{
			Valid:  false,
			Errors: []string{err.Error()},
		}, nil
	}

	result := &ValidationResult{
		Valid:    true,
		Warnings: policy.Warnings,
	}

	// Add validation checks
	if policy.ResourceLimits.CPULimit > 8.0 {
		result.Warnings = append(result.Warnings, "CPU limit > 8 cores may be excessive")
	}
	if policy.ResourceLimits.MemoryLimitMB > 16384 {
		result.Warnings = append(result.Warnings, "Memory limit > 16GB may be excessive")
	}
	if policy.NetworkPolicy.Enabled && len(policy.NetworkPolicy.AllowedHosts) == 0 {
		result.Warnings = append(result.Warnings, "Network enabled with no host restrictions")
	}
	if !policy.CommandPolicy.BlockSudo {
		result.Warnings = append(result.Warnings, "sudo is not blocked - security risk")
	}

	return result, nil
}

// MockParser is a simple parser for testing that doesn't require API keys
type MockParser struct{}

// NewMockParser creates a mock parser for testing
func NewMockParser() *MockParser {
	return &MockParser{}
}

// Parse returns a predefined policy
func (m *MockParser) Parse(ctx context.Context, naturalLanguage string) (*Policy, error) {
	// Simple heuristic-based parsing for testing
	policy := &Policy{
		ResourceLimits: ResourceLimits{
			CPULimit:       1.0,
			MemoryLimitMB:  512,
			TimeoutSeconds: 300,
		},
		NetworkPolicy: NetworkPolicy{
			Enabled:       false,
			AllowDNS:      false,
			AllowLoopback: false,
		},
		CommandPolicy: CommandPolicy{
			BlockSudo:    true,
			BlockNetwork: true,
		},
		FilePolicy: FilePolicy{
			AllowedPaths: []string{"/workspace"},
			BlockedPaths: []string{"/etc", "/root", "/proc"},
		},
		Explanation: "Basic restricted policy (generated by mock parser)",
		GeneratedAt: time.Now(),
	}

	return policy, nil
}

// Validate always returns valid for mock parser
func (m *MockParser) Validate(ctx context.Context, naturalLanguage string) (*ValidationResult, error) {
	return &ValidationResult{
		Valid: true,
		Warnings: []string{"Using mock parser - not a real LLM parse"},
	}, nil
}
