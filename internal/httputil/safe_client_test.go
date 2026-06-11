package httputil

import (
	"net"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		// public
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
		// loopback / private / link-local
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // AWS/GCP IMDS
		{"0.0.0.0", true},
		{"100.64.0.1", true}, // CGNAT
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		// IPv4-mapped IPv6 forms of internal addresses must also be blocked
		{"::ffff:127.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"::ffff:10.0.0.1", true},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("could not parse %q", tc.ip)
		}
		if got := isPrivateIP(ip); got != tc.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestValidatePublicURLRejectsScheme(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "gopher://x", "ftp://x"} {
		if err := ValidatePublicURL(u, false); err == nil {
			t.Errorf("expected rejection for scheme in %q", u)
		}
	}
}

func TestValidatePublicURLRequireHTTPS(t *testing.T) {
	if err := ValidatePublicURL("http://example.com", true); err == nil {
		t.Error("expected http:// to be rejected when requireHTTPS=true")
	}
}
