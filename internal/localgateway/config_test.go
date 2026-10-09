package localgateway

import (
	"testing"
	"time"
)

func TestConfigValidateRejectsWeakToken(t *testing.T) {
	cfg := testConfig()
	cfg.AuthToken = "short"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for short auth token")
	}
}

func TestConfigValidateRejectsMissingVaultRun(t *testing.T) {
	cfg := testConfig()
	cfg.VaultRunBaseURL = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing VAULTRUN_BASE_URL")
	}
}

func TestValidateUpstreamURL(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"http://127.0.0.1:11434", false},
		{"http://localhost:1234", false},
		{"https://llm.example.com", false},
		{"", true},
		{"ftp://127.0.0.1", true},
		{"http://user:pass@127.0.0.1:11434", true},
		{"http://metadata.google.internal/", true},
		{"http://metadata/", true},
	}
	for _, tc := range cases {
		err := validateUpstreamURL(tc.url)
		if tc.wantErr && err == nil {
			t.Errorf("validateUpstreamURL(%q) = nil, want error", tc.url)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validateUpstreamURL(%q) = %v, want nil", tc.url, err)
		}
	}
}

func TestConfigBounds(t *testing.T) {
	cfg := testConfig()
	cfg.MaxToolLoops = 100
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected max tool loops bound")
	}
	cfg = testConfig()
	cfg.UpstreamTimeout = 100 * time.Millisecond
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected upstream timeout lower bound")
	}
}

func TestNormalizeListenAddrDefaultsToLoopback(t *testing.T) {
	cases := map[string]string{
		"8091":            "127.0.0.1:8091",
		"127.0.0.1:8091":  "127.0.0.1:8091",
		":8091":           ":8091", // explicit all-interfaces
		"0.0.0.0:8091":    "0.0.0.0:8091",
		"":                "127.0.0.1:8091",
	}
	for in, want := range cases {
		if got := normalizeListenAddr(in); got != want {
			t.Errorf("normalizeListenAddr(%q)=%q want %q", in, got, want)
		}
	}
}

func TestListenAddrExposesNonLoopback(t *testing.T) {
	if ListenAddrExposesNonLoopback("127.0.0.1:8091") {
		t.Fatal("loopback should not warn")
	}
	if ListenAddrExposesNonLoopback("localhost:8091") {
		t.Fatal("localhost should not warn")
	}
	if !ListenAddrExposesNonLoopback(":8091") {
		t.Fatal(":port should warn")
	}
	if !ListenAddrExposesNonLoopback("0.0.0.0:8091") {
		t.Fatal("0.0.0.0 should warn")
	}
}
