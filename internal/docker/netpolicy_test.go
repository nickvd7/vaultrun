package docker

import (
	"fmt"
	"strings"
	"testing"
)

func TestChainName(t *testing.T) {
	cases := []struct {
		containerID string
		want        string
	}{
		// Full 64-char container ID → truncated to first 12 chars.
		{
			containerID: "abcdef123456789012345678901234567890123456789012345678901234abcd",
			want:        "vr-abcdef123456",
		},
		// Short ID (< 12 chars) → used as-is.
		{
			containerID: "short",
			want:        "vr-short",
		},
		// Exactly 12 chars.
		{
			containerID: "abcdef123456",
			want:        "vr-abcdef123456",
		},
	}
	for _, tc := range cases {
		got := chainName(tc.containerID)
		if got != tc.want {
			t.Errorf("chainName(%q) = %q, want %q", tc.containerID, got, tc.want)
		}
		// Chain name must be ≤ 28 chars (iptables limit).
		if len(got) > 28 {
			t.Errorf("chainName(%q) = %q exceeds 28-char iptables limit (%d chars)", tc.containerID, got, len(got))
		}
	}
}

func TestBridgeIface(t *testing.T) {
	cases := []struct {
		networkID string
		want      string
	}{
		{
			networkID: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			want:      "br-abcdef123456",
		},
		{
			networkID: "short",
			want:      "br-short",
		},
	}
	for _, tc := range cases {
		got := bridgeIface(tc.networkID)
		if got != tc.want {
			t.Errorf("bridgeIface(%q) = %q, want %q", tc.networkID, got, tc.want)
		}
	}
}

func TestChainNamePrefix(t *testing.T) {
	// All chain names must start with the "vr-" prefix.
	got := chainName("abc123def456789")
	if !strings.HasPrefix(got, "vr-") {
		t.Errorf("chainName must start with 'vr-', got %q", got)
	}
}

func TestBridgeIfacePrefix(t *testing.T) {
	got := bridgeIface("abc123def456789")
	if !strings.HasPrefix(got, "br-") {
		t.Errorf("bridgeIface must start with 'br-', got %q", got)
	}
}

func TestIsIPTExistsErr(t *testing.T) {
	cases := []struct {
		errMsg string
		want   bool
	}{
		{"Chain already exists", true},
		{"Already exists", true},
		{"already exists", true},
		{"Duplicate rule", true},
		{"iptables: No chain/target/match by that name.", false},
		{"", false},
	}
	for _, tc := range cases {
		var err error
		if tc.errMsg != "" {
			err = fmt.Errorf("%s", tc.errMsg) //nolint:goerr113
		}
		got := isIPTExistsErr(err)
		if got != tc.want {
			t.Errorf("isIPTExistsErr(%q) = %v, want %v", tc.errMsg, got, tc.want)
		}
	}
}
