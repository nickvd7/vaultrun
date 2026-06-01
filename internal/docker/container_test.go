package docker

import (
	"context"
	"testing"
)

// TestResolveToIPsRawIP verifies that public raw IP addresses pass through unchanged.
func TestResolveToIPsRawIP(t *testing.T) {
	ips, err := resolveToIPs([]string{"1.2.3.4", "8.8.8.8"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("want 2 IPs, got %d: %v", len(ips), ips)
	}
	if ips[0] != "1.2.3.4" {
		t.Errorf("want 1.2.3.4, got %q", ips[0])
	}
	if ips[1] != "8.8.8.8" {
		t.Errorf("want 8.8.8.8, got %q", ips[1])
	}
}

// TestResolveToIPsRejectsPrivate verifies that private/internal IPs are rejected
// to prevent DNS-rebinding attacks that could allow containers to reach the host
// metadata service or internal network services.
func TestResolveToIPsRejectsPrivate(t *testing.T) {
	privateIPs := []string{
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"127.0.0.1",
		"169.254.169.254", // AWS IMDS
	}
	for _, ip := range privateIPs {
		_, err := resolveToIPs([]string{ip})
		if err == nil {
			t.Errorf("expected rejection for private IP %q, got nil error", ip)
		}
	}
}

// TestResolveToIPsDeduplicates ensures the same IP from multiple entries
// only appears once.
func TestResolveToIPsDeduplicates(t *testing.T) {
	ips, err := resolveToIPs([]string{"1.2.3.4", "1.2.3.4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 1 {
		t.Errorf("want 1 (deduplicated) IP, got %d: %v", len(ips), ips)
	}
}

// TestResolveToIPsEmpty returns nil for an empty host list.
func TestResolveToIPsEmpty(t *testing.T) {
	ips, err := resolveToIPs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 0 {
		t.Errorf("want empty, got %v", ips)
	}
}

// TestVerifyImageNoOpWhenKeyNotSet verifies that VerifyImage is a no-op when
// no cosign public key is configured (the common dev case).
func TestVerifyImageNoOpWhenKeyNotSet(t *testing.T) {
	c := &Client{cosignPublicKey: ""}
	ref, err := c.VerifyImage(context.Background(), "python:3.12-slim")
	if err != nil {
		t.Errorf("expected no-op when cosignPublicKey is empty, got err: %v", err)
	}
	if ref != "python:3.12-slim" {
		t.Errorf("expected original image ref when verification is disabled, got %q", ref)
	}
}

// TestVerifyImageFailsClosedWhenBinaryMissing verifies that VerifyImage fails
// closed (returns an error) when a key is set but the cosign binary is absent.
func TestVerifyImageFailsClosedWhenBinaryMissing(t *testing.T) {
	c := &Client{cosignPublicKey: "/nonexistent/key.pem"}
	_, err := c.VerifyImage(context.Background(), "python:3.12-slim")
	if err == nil {
		t.Error("expected error when cosign binary is missing, got nil")
	}
}

// TestBridgeIfaceMatchesDockerConvention verifies that bridgeIface produces
// the same naming convention Docker uses for custom bridge networks.
func TestBridgeIfaceMatchesDockerConvention(t *testing.T) {
	// Docker names a custom network's bridge interface "br-<networkID[:12]>"
	networkID := "abc123def456789012345678"
	want := "br-abc123def456"
	if got := bridgeIface(networkID); got != want {
		t.Errorf("bridgeIface(%q) = %q, want %q", networkID, got, want)
	}
}

func TestExtractCosignDigest(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name: "clean JSON on its own line",
			output: `[{"critical":{"identity":{"docker-reference":"docker.io/library/python"}` +
				`,"image":{"docker-manifest-digest":"sha256:abc123def456"}` +
				`,"type":"cosign container image signature"},"optional":null}]`,
			want: "sha256:abc123def456",
		},
		{
			name: "JSON preceded by informational text",
			output: "Verification for docker.io/library/python:3.12-slim --\n" +
				"The following checks were performed on each of these signatures:\n" +
				`[{"critical":{"identity":{"docker-reference":"docker.io/library/python"}` +
				`,"image":{"docker-manifest-digest":"sha256:deadbeef"}` +
				`,"type":"cosign container image signature"},"optional":null}]`,
			want: "sha256:deadbeef",
		},
		{
			name:    "no JSON at all",
			output:  "some random output without json",
			wantErr: true,
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractCosignDigest([]byte(tc.output))
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPinnedImageRef(t *testing.T) {
	digest := "sha256:abc123"
	cases := []struct {
		image string
		want  string
	}{
		{"python:3.12-slim", "python@sha256:abc123"},
		{"docker.io/library/python:3.12-slim", "docker.io/library/python@sha256:abc123"},
		{"registry:5000/myimage:v1", "registry:5000/myimage@sha256:abc123"},
		{"myimage", "myimage@sha256:abc123"},
		{"myimage@sha256:old", "myimage@sha256:abc123"},
	}
	for _, tc := range cases {
		got := pinnedImageRef(tc.image, digest)
		if got != tc.want {
			t.Errorf("pinnedImageRef(%q, %q) = %q, want %q", tc.image, digest, got, tc.want)
		}
	}
}
