package docker

import (
	"errors"
	"testing"
)

func TestValidateAllowedHosts(t *testing.T) {
	good := []string{"example.com", "api.github.com", "93.184.216.34", "8.8.8.0/24", "Example.COM.", "a-b.example.org"}
	for _, h := range good {
		if err := ValidateAllowedHosts([]string{h}); err != nil {
			t.Errorf("%q rejected: %v", h, err)
		}
	}
	bad := []string{
		"", " example.com", "localhost", "foo.localhost", "metadata.google.internal", "printer.local",
		"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1",
		"10.0.0.0/8", "0.0.0.0/0", "192.168.0.0/15", "8.8.8.0/99",
		"http://example.com", "exa mple.com", "example.com;rm -rf", "-bad.com", "a..b",
	}
	for _, h := range bad {
		err := ValidateAllowedHosts([]string{h})
		if err == nil || !errors.Is(err, ErrInvalidAllowedHosts) {
			t.Errorf("%q accepted or wrong error: %v", h, err)
		}
	}
	many := make([]string, maxAllowedHosts+1)
	for i := range many {
		many[i] = "example.com"
	}
	if err := ValidateAllowedHosts(many); err == nil {
		t.Error("too many hosts accepted")
	}
}

func TestResolveToIPsCIDR(t *testing.T) {
	got, err := resolveToIPs([]string{"8.8.8.0/24", "1.1.1.1", "8.8.8.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "8.8.8.0/24" || got[1] != "1.1.1.1" {
		t.Fatalf("got %v", got)
	}
	if _, err := resolveToIPs([]string{"10.0.0.0/8"}); !errors.Is(err, ErrInvalidAllowedHosts) {
		t.Fatalf("private CIDR: %v", err)
	}
	if _, err := resolveToIPs([]string{"127.0.0.1"}); !errors.Is(err, ErrInvalidAllowedHosts) {
		t.Fatalf("loopback: %v", err)
	}
}

func TestNormalizeWorkingDir(t *testing.T) {
	ok := map[string]string{"": "/workspace", "/workspace": "/workspace", "/workspace/": "/workspace", "/workspace//a": "/workspace/a",
		"/workspace/./x": "/workspace/x", "/workspace/a/../b": "/workspace/b", "/tmp": "/tmp", "/tmp/x": "/tmp/x"}
	for in, want := range ok {
		if got, err := NormalizeWorkingDir(in); err != nil || got != want {
			t.Errorf("NormalizeWorkingDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"relative", "/etc", "/workspace/../etc", "/workspace/a/../../etc", "/tmp/..", "/workspacex", "/proc/self"} {
		if _, err := NormalizeWorkingDir(in); err == nil {
			t.Errorf("NormalizeWorkingDir(%q) accepted", in)
		}
	}
}
