package docker

import (
	"context"
	"testing"
)

// TestResolveToIPsRawIP verifies that raw IP addresses pass through unchanged.
func TestResolveToIPsRawIP(t *testing.T) {
	ips := resolveToIPs([]string{"1.2.3.4", "10.0.0.1"})
	if len(ips) != 2 {
		t.Fatalf("want 2 IPs, got %d: %v", len(ips), ips)
	}
	if ips[0] != "1.2.3.4" {
		t.Errorf("want 1.2.3.4, got %q", ips[0])
	}
	if ips[1] != "10.0.0.1" {
		t.Errorf("want 10.0.0.1, got %q", ips[1])
	}
}

// TestResolveToIPsDeduplicates ensures the same IP from multiple entries
// only appears once.
func TestResolveToIPsDeduplicates(t *testing.T) {
	ips := resolveToIPs([]string{"1.2.3.4", "1.2.3.4"})
	if len(ips) != 1 {
		t.Errorf("want 1 (deduplicated) IP, got %d: %v", len(ips), ips)
	}
}

// TestResolveToIPsEmpty returns nil for an empty host list.
func TestResolveToIPsEmpty(t *testing.T) {
	ips := resolveToIPs(nil)
	if len(ips) != 0 {
		t.Errorf("want empty, got %v", ips)
	}
}

// TestVerifyImageNoOpWhenKeyNotSet verifies that VerifyImage is a no-op when
// no cosign public key is configured (the common dev case).
func TestVerifyImageNoOpWhenKeyNotSet(t *testing.T) {
	c := &Client{cosignPublicKey: ""}
	if err := c.VerifyImage(context.Background(), "python:3.12-slim"); err != nil {
		t.Errorf("expected no-op when cosignPublicKey is empty, got err: %v", err)
	}
}

// TestVerifyImageFailsClosedWhenBinaryMissing verifies that VerifyImage fails
// closed (returns an error) when a key is set but the cosign binary is absent.
func TestVerifyImageFailsClosedWhenBinaryMissing(t *testing.T) {
	c := &Client{cosignPublicKey: "/nonexistent/key.pem"}
	err := c.VerifyImage(context.Background(), "python:3.12-slim")
	if err == nil {
		t.Error("expected error when cosign binary is missing, got nil")
	}
}
