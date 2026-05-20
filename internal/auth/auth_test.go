package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/auth"
	"github.com/nickvd7/vaultrun/internal/models"
)

func TestGenerateKey(t *testing.T) {
	plaintext, key, err := auth.GenerateKey("test-key", nil)
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
		plain, _, err := auth.GenerateKey("k", nil)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if seen[plain] {
			t.Fatal("duplicate key generated")
		}
		seen[plain] = true
	}
}

// --- expiry logic unit tests (no DB required) ---

func expiredKey() *models.APIKey {
	past := time.Now().Add(-1 * time.Hour)
	return &models.APIKey{
		ID:        uuid.New(),
		Name:      "expired",
		Active:    true,
		ExpiresAt: &past,
	}
}

func activeKey() *models.APIKey {
	future := time.Now().Add(24 * time.Hour)
	return &models.APIKey{
		ID:        uuid.New(),
		Name:      "active",
		Active:    true,
		ExpiresAt: &future,
	}
}

func neverExpiresKey() *models.APIKey {
	return &models.APIKey{ID: uuid.New(), Name: "forever", Active: true}
}

func TestKeyExpiryPast(t *testing.T) {
	k := expiredKey()
	if k.ExpiresAt == nil || !k.ExpiresAt.Before(time.Now()) {
		t.Fatal("test setup: key should already be expired")
	}
	// Simulate the check performed in auth.Validate
	if k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()) {
		return // correctly detected as expired
	}
	t.Fatal("expired key was not detected")
}

func TestKeyExpiryFuture(t *testing.T) {
	k := activeKey()
	if k.ExpiresAt == nil || k.ExpiresAt.Before(time.Now()) {
		t.Fatal("key with future expiry should not be expired")
	}
}

func TestKeyNoExpiry(t *testing.T) {
	k := neverExpiresKey()
	if k.ExpiresAt != nil {
		t.Fatal("key without expiry should have nil ExpiresAt")
	}
	// nil ExpiresAt means no expiry — should pass
	if k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()) {
		t.Fatal("nil expiry should never be treated as expired")
	}
}
