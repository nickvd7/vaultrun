package auth_test

import (
	"strings"
	"testing"

	"github.com/nickvd7/vaultrun/internal/auth"
)

func TestGenerateKey(t *testing.T) {
	plaintext, key, err := auth.GenerateKey("test-key")
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if !strings.HasPrefix(plaintext, "vr_") {
		t.Errorf("key should start with 'vr_', got %q", plaintext[:8])
	}
	if len(plaintext) < 20 {
		t.Errorf("key too short: %d chars", len(plaintext))
	}
	if key.KeyHash == "" {
		t.Error("KeyHash should not be empty")
	}
	if key.Prefix == "" {
		t.Error("Prefix should not be empty")
	}
	if strings.HasPrefix(key.Prefix, "vr_") {
		// Good
	}
	if key.Name != "test-key" {
		t.Errorf("Name = %q, want %q", key.Name, "test-key")
	}
}

func TestHashKeyConsistency(t *testing.T) {
	plain := "vr_testkey12345"
	h1 := auth.HashKey(plain)
	h2 := auth.HashKey(plain)
	if h1 != h2 {
		t.Error("HashKey should produce consistent results")
	}
}

func TestHashKeyDifferentInputs(t *testing.T) {
	h1 := auth.HashKey("vr_key1")
	h2 := auth.HashKey("vr_key2")
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestGenerateKeyUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		plain, _, err := auth.GenerateKey("k")
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if seen[plain] {
			t.Fatal("duplicate key generated")
		}
		seen[plain] = true
	}
}
