package docker

import (
	"errors"
	"strings"
	"testing"
)

func TestIsImageUnavailable(t *testing.T) {
	cases := map[string]bool{
		"Error response from daemon: invalid reference format: repository name must be lowercase": true,
		"pull access denied for nope, repository does not exist or may require authorization":     true,
		"manifest unknown":                    true,
		"Cannot connect to the Docker daemon": false,
		"context deadline exceeded":           false,
	}
	for msg, want := range cases {
		if got := isImageUnavailable(errors.New(msg)); got != want {
			t.Errorf("isImageUnavailable(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestOOMMessage(t *testing.T) {
	var nilRes *ExecResult
	if nilRes.OOMMessage() != "" || (&ExecResult{ExitCode: 137}).OOMMessage() != "" {
		t.Fatal("no message expected unless OOMKilled")
	}
	msg := (&ExecResult{OOMKilled: true, MemoryLimitMB: 128}).OOMMessage()
	if !strings.Contains(msg, "memory limit (128 MB)") {
		t.Fatalf("unexpected message: %q", msg)
	}
	if !strings.Contains((&ExecResult{OOMKilled: true}).OOMMessage(), "memory limit") {
		t.Fatal("message without limit should still mention the memory limit")
	}
}
