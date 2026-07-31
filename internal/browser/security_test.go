package browser

import (
	"net"
	"strings"
	"testing"
)

// newTestManager returns a manager with the secure default policy and no
// Docker/DB dependencies — validateURL only reads m.policy.
func newTestManager() *PlaywrightManager {
	return &PlaywrightManager{policy: DefaultNetworkPolicy()}
}

// TestSSRFProtection verifies that private IPs and cloud metadata endpoints are blocked.
// This is the single most important security control for the browser layer: without it
// an agent could reach the host network, internal services, or cloud credentials.
func TestSSRFProtection(t *testing.T) {
	blocked := []struct {
		name string
		url  string
	}{
		// Loopback
		{"localhost hostname", "http://localhost/admin"},
		{"localhost with port", "http://localhost:8080/admin"},
		{"127.0.0.1", "http://127.0.0.1/"},
		{"127.0.0.1 alt port", "http://127.0.0.1:5432/"},
		{"127.x.x.x range", "http://127.1.2.3/"},
		{"IPv6 loopback", "http://[::1]/"},
		{"IPv6 loopback port", "http://[::1]:22/"},

		// RFC1918 private ranges
		{"10.x private", "http://10.0.0.1/"},
		{"10.x deep", "http://10.255.255.255/"},
		{"172.16 private", "http://172.16.0.1/"},
		{"172.31 private edge", "http://172.31.255.255/"},
		{"192.168 private", "http://192.168.1.1/"},
		{"192.168 router", "http://192.168.0.1/admin"},

		// Link-local / cloud metadata
		{"AWS metadata IP", "http://169.254.169.254/latest/meta-data/"},
		{"AWS metadata creds", "http://169.254.169.254/latest/meta-data/iam/security-credentials/"},
		{"link-local range", "http://169.254.1.1/"},
		{"GCP metadata host", "http://metadata.google.internal/computeMetadata/v1/"},
		{"GCP metadata short", "http://metadata/computeMetadata/v1/"},

		// Carrier-grade NAT and other reserved ranges
		{"100.64 CGNAT", "http://100.64.0.1/"},
		{"0.0.0.0", "http://0.0.0.0/"},

		// Dangerous schemes
		{"file scheme", "file:///etc/passwd"},
		{"file scheme shadow", "file:///etc/shadow"},
		{"javascript scheme", "javascript:alert(1)"},
		{"data scheme", "data:text/html,<script>alert(1)</script>"},
		{"gopher scheme", "gopher://127.0.0.1:6379/_INFO"},
		{"ftp scheme", "ftp://internal.example.com/"},
	}

	m := newTestManager()

	for _, tc := range blocked {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.validateURL(tc.url); err == nil {
				t.Errorf("validateURL(%q) = nil, want error — SSRF protection bypassed", tc.url)
			}
		})
	}
}

// TestSSRFAllowsPublicURLs verifies legitimate public URLs still work — an
// over-eager blocklist would make the browser layer useless.
func TestSSRFAllowsPublicURLs(t *testing.T) {
	allowed := []string{
		"https://example.com/",
		"https://github.com/nickvd7/vaultrun",
		"http://example.com:8080/path?query=1",
		"https://api.github.com/repos/foo/bar",
		"https://1.1.1.1/", // public DNS resolver, not private
		"https://8.8.8.8/",
	}

	m := newTestManager()

	for _, url := range allowed {
		t.Run(url, func(t *testing.T) {
			if err := m.validateURL(url); err != nil {
				t.Errorf("validateURL(%q) = %v, want nil — legitimate URL blocked", url, err)
			}
		})
	}
}

// TestIsPrivateIP checks the IP classifier directly across boundary values.
// Off-by-one errors at range edges are the classic way SSRF filters fail.
func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
	}{
		// 10.0.0.0/8 boundaries
		{"9.255.255.255", false},
		{"10.0.0.0", true},
		{"10.255.255.255", true},
		{"11.0.0.0", false},

		// 172.16.0.0/12 boundaries
		{"172.15.255.255", false},
		{"172.16.0.0", true},
		{"172.31.255.255", true},
		{"172.32.0.0", false},

		// 192.168.0.0/16 boundaries
		{"192.167.255.255", false},
		{"192.168.0.0", true},
		{"192.168.255.255", true},
		{"192.169.0.0", false},

		// Loopback
		{"127.0.0.1", true},
		{"127.255.255.255", true},

		// Link-local
		{"169.254.0.1", true},
		{"169.254.169.254", true},

		// Public
		{"1.1.1.1", false},
		{"8.8.8.8", false},
		{"93.184.216.34", false},

		// IPv6
		{"::1", true},
		{"fe80::1", true},   // link-local
		{"fc00::1", true},   // unique local
		{"2606:4700::1", false}, // public Cloudflare
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("could not parse test IP %q", tc.ip)
			}
			if got := isPrivateIP(ip); got != tc.private {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tc.ip, got, tc.private)
			}
		})
	}
}

// TestIsMetadataIP verifies cloud metadata endpoints are recognised. These leak
// IAM credentials, so they must be blocked even though 169.254.x is also caught
// by the link-local check (defence in depth).
func TestIsMetadataIP(t *testing.T) {
	cases := []struct {
		ip       string
		metadata bool
	}{
		{"169.254.169.254", true}, // AWS / Azure / DigitalOcean
		{"169.254.170.2", true},   // AWS ECS task metadata
		{"1.1.1.1", false},
		{"10.0.0.1", false},
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("could not parse test IP %q", tc.ip)
			}
			if got := isMetadataIP(ip); got != tc.metadata {
				t.Errorf("isMetadataIP(%s) = %v, want %v", tc.ip, got, tc.metadata)
			}
		})
	}
}

// TestEscapeStringPreventsInjection verifies that user-supplied selectors and
// values cannot break out of the generated Python string literals. Playwright
// scripts are assembled as Python source, so a naive quote would let an agent
// execute arbitrary Python inside the sandbox.
func TestEscapeStringPreventsInjection(t *testing.T) {
	inputs := []struct {
		name  string
		input string
	}{
		{"single quote breakout", `'; import os; os.system('whoami')`},
		{"double quote breakout", `"; import os; os.system("id")`},
		{"triple quote breakout", `"""` + "\n" + `import os` + "\n" + `"""`},
		{"newline injection", "value\nimport os\nos.system('id')"},
		{"carriage return injection", "value\rimport os"},
		{"backslash then quote", `back\'slash`},
		{"trailing backslash", `value\`},
		{"NUL byte", "value\x00truncated"},
		{"unicode line separator", "value\u2028import os"},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeString(tc.input)

			// The output is interpolated into a single-quoted Python literal.
			// It is safe exactly when every quote, backslash and newline it
			// contains is preceded by an odd number of backslashes (i.e. is
			// itself escaped) — verified by scanning the result.
			assertPythonLiteralSafe(t, tc.input, got)
		})
	}
}

// assertPythonLiteralSafe walks escaped and fails if any character that could
// terminate or extend a single-quoted Python string literal appears unescaped.
func assertPythonLiteralSafe(t *testing.T, input, escaped string) {
	t.Helper()

	runes := []rune(escaped)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if r == '\\' {
			// Skip the escape sequence: the next rune is escaped by this one.
			i++
			continue
		}

		// Reaching here means r was not preceded by a backslash.
		switch r {
		case '\'':
			t.Errorf("escapeString(%q) = %q — unescaped single quote at index %d", input, escaped, i)
		case '\n':
			t.Errorf("escapeString(%q) = %q — raw newline at index %d", input, escaped, i)
		case '\r':
			t.Errorf("escapeString(%q) = %q — raw carriage return at index %d", input, escaped, i)
		case 0:
			t.Errorf("escapeString(%q) = %q — raw NUL byte at index %d", input, escaped, i)
		}
	}

	// A literal backslash in the input must survive as a doubled backslash,
	// otherwise it would consume the escape of whatever follows it.
	if strings.Contains(input, `\`) && !strings.Contains(escaped, `\\`) {
		t.Errorf("escapeString(%q) = %q — backslash not doubled", input, escaped)
	}
}

// TestDefaultNetworkPolicyIsRestrictive verifies the zero-configuration policy
// denies private network access. A permissive default would silently expose
// every deployment that never configures a policy.
func TestDefaultNetworkPolicyIsRestrictive(t *testing.T) {
	p := DefaultNetworkPolicy()

	if !p.BlockPrivateIPs {
		t.Error("DefaultNetworkPolicy().BlockPrivateIPs = false, want true")
	}
	if !p.BlockMetadata {
		t.Error("DefaultNetworkPolicy().BlockMetadata = false, want true")
	}
}
