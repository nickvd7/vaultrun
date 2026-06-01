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
		{
			containerID: "abcdef123456789012345678901234567890123456789012345678901234abcd",
			want:        "vr-abcdef123456",
		},
		{
			containerID: "short",
			want:        "vr-short",
		},
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

func TestIsIPT6NotFound(t *testing.T) {
	cases := []struct {
		errMsg string
		want   bool
	}{
		{"executable file not found in $PATH", true},
		{"no such file or directory", true},
		{"ip6tables: Chain already exists", false},
		{"", false},
	}
	for _, tc := range cases {
		var err error
		if tc.errMsg != "" {
			err = fmt.Errorf("%s", tc.errMsg) //nolint:goerr113
		}
		got := isIPT6NotFound(err)
		if got != tc.want {
			t.Errorf("isIPT6NotFound(%q) = %v, want %v", tc.errMsg, got, tc.want)
		}
	}
}

// TestApplyEgressPolicyInvalidDNSServer verifies that applyEgressPolicy rejects
// invalid dnsServer values before any iptables calls are made.
func TestApplyEgressPolicyInvalidDNSServer(t *testing.T) {
	cases := []string{"", "not-an-ip", "256.0.0.1", "::xyz"}
	for _, dns := range cases {
		err := applyEgressPolicy("vr-abc123def456", "br-abc123def456", dns, nil)
		if err == nil {
			t.Errorf("dnsServer=%q: expected error, got nil", dns)
			continue
		}
		if !strings.Contains(err.Error(), "invalid DNS server IP") {
			t.Errorf("dnsServer=%q: want 'invalid DNS server IP' in error, got: %v", dns, err)
		}
	}
}

// TestApplyEgressPolicyValidIPPassesValidation ensures a valid gateway IP
// passes validation (subsequent iptables call will fail in test environment
// but must NOT produce the IP-validation error).
func TestApplyEgressPolicyValidIPPassesValidation(t *testing.T) {
	err := applyEgressPolicy("vr-abc123def456", "br-abc123def456", "172.17.0.1", nil)
	if err != nil && strings.Contains(err.Error(), "invalid DNS server IP") {
		t.Errorf("valid gateway IP rejected: %v", err)
	}
}
