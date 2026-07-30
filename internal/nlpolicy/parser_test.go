package nlpolicy

import (
	"context"
	"testing"
)

func TestMockParser(t *testing.T) {
	parser := NewMockParser()

	policy, err := parser.Parse(context.Background(), "Allow github.com, max 2 CPU")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if policy == nil {
		t.Fatal("Policy is nil")
	}

	if policy.ResourceLimits.CPULimit <= 0 {
		t.Error("CPU limit should be positive")
	}

	if policy.ResourceLimits.MemoryLimitMB <= 0 {
		t.Error("Memory limit should be positive")
	}

	if policy.Explanation == "" {
		t.Error("Explanation should not be empty")
	}
}

func TestMockParserValidate(t *testing.T) {
	parser := NewMockParser()

	result, err := parser.Validate(context.Background(), "Some policy text")
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	if result == nil {
		t.Fatal("ValidationResult is nil")
	}

	if !result.Valid {
		t.Error("Mock parser should always return valid")
	}
}

func TestPolicyDefaults(t *testing.T) {
	parser := NewMockParser()

	policy, err := parser.Parse(context.Background(), "Basic policy")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check defaults
	if policy.CommandPolicy.BlockSudo != true {
		t.Error("BlockSudo should default to true")
	}

	if len(policy.FilePolicy.AllowedPaths) == 0 {
		t.Error("Should have at least one allowed path")
	}

	if len(policy.FilePolicy.BlockedPaths) == 0 {
		t.Error("Should have blocked paths for security")
	}
}
