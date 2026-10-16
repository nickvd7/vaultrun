package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nickvd7/vaultrun/internal/config"
)

func TestCommandMustBeTheExecutableOnly(t *testing.T) {
	cases := []struct {
		command string
		ok      bool
	}{
		{"python", true},
		{"/usr/bin/env", true},
		{"python -c print(2+2)", false},
		{"ls -la", false},
		{"echo\thi", false},
		{"", false},
		{"   ", false},
	}
	for _, tc := range cases {
		c, w := newPolicyTestContext(http.MethodPost, "/run", "")
		got := validateCommand(c, tc.command)
		if got != tc.ok {
			t.Errorf("validateCommand(%q) = %v, want %v", tc.command, got, tc.ok)
		}
		if !tc.ok {
			if w.Code != http.StatusBadRequest {
				t.Errorf("%q: status %d, want 400", tc.command, w.Code)
			}
			if tc.command != "" && strings.TrimSpace(tc.command) != "" && !strings.Contains(w.Body.String(), "args") {
				t.Errorf("%q: message should point at args: %s", tc.command, w.Body.String())
			}
		}
	}
}

func TestAllowedHostsRequireNetwork(t *testing.T) {
	cfg := &config.Config{Docker: config.DockerConfig{DefaultImage: "python:3.12-slim"}}
	cfg.Workspace.BaseDir = t.TempDir()
	h := NewHub(nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil)

	c, w := newPolicyTestContext(http.MethodPost, "/sessions", `{"allowed_hosts":["pypi.org"]}`)
	NewSessionHandler(h).Create(c)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "network_enabled") {
		t.Fatalf("allowed_hosts without network_enabled: %d %s", w.Code, w.Body.String())
	}
}
