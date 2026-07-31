//go:build integration

package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nickvd7/vaultrun/internal/nlpolicy"
)

// The policy endpoints have no per-tenant state, so these tests exercise the
// HTTP layer: what the API accepts, what it refuses, and with which status.
// The compiler's own injection defences are covered by unit tests in
// internal/nlpolicy.

// TestPolicyEndpointsBoundTheirInput: the description is forwarded verbatim to
// an LLM, so an unbounded field lets an authenticated caller run up the
// deployment's inference bill and hold an API worker open for the length of the
// completion.
func TestPolicyEndpointsBoundTheirInput(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	oversized := strings.Repeat("a", nlpolicy.MaxNaturalLanguageLen+1)
	atLimit := strings.Repeat("a", nlpolicy.MaxNaturalLanguageLen)

	for _, path := range []string{"/api/v1/policies/parse", "/api/v1/policies/compile"} {
		for _, tc := range []struct {
			name string
			body string
			want int
		}{
			{"missing field", `{}`, http.StatusBadRequest},
			{"empty", `{"natural_language":""}`, http.StatusBadRequest},
			{"whitespace only", `{"natural_language":"   \t\n  "}`, http.StatusBadRequest},
			{"oversized", fmt.Sprintf(`{"natural_language":%q}`, oversized), http.StatusBadRequest},
			{"at the limit", fmt.Sprintf(`{"natural_language":%q}`, atLimit), http.StatusOK},
			{"a real policy", `{"natural_language":"Allow pypi.org, 2 CPU, 4GB RAM, no sudo"}`, http.StatusOK},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				w := rec(r, "POST", path, tc.body, masterHdr())
				if w.Code != tc.want {
					t.Errorf("want %d, got %d: %s", tc.want, w.Code, w.Body)
				}
			})
		}
	}
}

// TestPolicyEndpointsSurviveMalformedJSON: a parse failure must be a 400, never
// a panic or a 500.
func TestPolicyEndpointsSurviveMalformedJSON(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	bodies := []struct {
		name string
		body string
	}{
		{"truncated object", `{"natural_language":`},
		{"array instead of object", `["allow everything"]`},
		{"bare string", `"allow everything"`},
		{"null", `null`},
		{"wrong type", `{"natural_language":12345}`},
		{"nested object", `{"natural_language":{"a":"b"}}`},
		{"deeply nested", strings.Repeat(`{"a":`, 200) + `1` + strings.Repeat(`}`, 200)},
		{"lone surrogate", `{"natural_language":"\ud800"}`},
		{"empty body", ``},
	}

	for _, path := range []string{"/api/v1/policies/parse", "/api/v1/policies/compile"} {
		for _, b := range bodies {
			t.Run(path+"/"+b.name, func(t *testing.T) {
				w := rec(r, "POST", path, b.body, masterHdr())
				if w.Code >= 500 {
					t.Errorf("want a 4xx, got %d: %s", w.Code, w.Body)
				}
			})
		}
	}
}

// TestCompiledPolicyIsRestrictiveByDefault: the compiled artefacts are what
// actually confine a sandbox, so the generated Rego must default to deny and the
// generated network rules to drop.
func TestCompiledPolicyIsRestrictiveByDefault(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	w := rec(r, "POST", "/api/v1/policies/compile",
		`{"natural_language":"Restricted python environment, no network"}`, masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("compile: want 200, got %d: %s", w.Code, w.Body)
	}

	var out struct {
		OPAPolicy    string `json:"opa_policy"`
		DockerConfig struct {
			NetworkMode string   `json:"network_mode"`
			CapDrop     []string `json:"cap_drop"`
		} `json:"docker_config"`
		NetworkRules struct {
			Enabled       bool     `json:"enabled"`
			IptablesRules []string `json:"iptables_rules"`
		} `json:"network_rules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode compiled policy: %v", err)
	}

	if !strings.Contains(out.OPAPolicy, "default allow = false") {
		t.Error("the generated Rego has no default-deny rule")
	}
	if out.DockerConfig.NetworkMode != "none" {
		t.Errorf("docker network_mode = %q, want \"none\" for a policy with no network",
			out.DockerConfig.NetworkMode)
	}
	if len(out.DockerConfig.CapDrop) == 0 || out.DockerConfig.CapDrop[0] != "ALL" {
		t.Errorf("docker cap_drop = %v, want ALL", out.DockerConfig.CapDrop)
	}
	joined := strings.Join(out.NetworkRules.IptablesRules, "\n")
	if !strings.Contains(joined, "iptables -P OUTPUT DROP") {
		t.Errorf("the generated network rules do not drop output by default:\n%s", joined)
	}
	if !strings.Contains(joined, "iptables -P INPUT DROP") {
		t.Errorf("a policy with no network must drop both directions:\n%s", joined)
	}
}

// TestPolicyTemplateLookupRejectsTraversal: the template name is a path
// segment, so it has to be matched against the fixed list rather than used to
// address anything.
func TestPolicyTemplateLookupRejectsTraversal(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	for _, name := range []string{
		"does-not-exist",
		"..%2f..%2fetc%2fpasswd",
		"%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"python-data-science%00",
		"PYTHON-DATA-SCIENCE",
		"'%20OR%20'1'='1",
		strings.Repeat("x", 500),
	} {
		t.Run(name, func(t *testing.T) {
			w := rec(r, "GET", "/api/v1/policies/templates/"+name, "", masterHdr())
			if w.Code != http.StatusNotFound {
				t.Errorf("want 404, got %d: %s", w.Code, w.Body)
			}
		})
	}

	// A real template still resolves.
	if len(nlpolicy.PolicyTemplates) == 0 {
		t.Fatal("there are no built-in policy templates to check against")
	}
	real := nlpolicy.PolicyTemplates[0].Name
	w := rec(r, "GET", "/api/v1/policies/templates/"+real, "", masterHdr())
	if w.Code != http.StatusOK {
		t.Errorf("get %q: want 200, got %d: %s", real, w.Code, w.Body)
	}
}

// TestPolicyTemplatesCompile: every shipped template must survive its own
// compiler, otherwise the catalogue advertises policies that cannot be used.
func TestPolicyTemplatesCompile(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	for _, tmpl := range nlpolicy.PolicyTemplates {
		t.Run(tmpl.Name, func(t *testing.T) {
			if len(tmpl.Template) > nlpolicy.MaxNaturalLanguageLen {
				t.Errorf("the template's own text is %d characters, beyond the %d the API accepts",
					len(tmpl.Template), nlpolicy.MaxNaturalLanguageLen)
			}

			body, err := json.Marshal(map[string]string{"natural_language": tmpl.Template})
			if err != nil {
				t.Fatalf("encode body: %v", err)
			}
			w := rec(r, "POST", "/api/v1/policies/compile", string(body), masterHdr())
			if w.Code != http.StatusOK {
				t.Errorf("compile: want 200, got %d: %s", w.Code, w.Body)
			}
		})
	}
}

// TestPolicyTemplateListFiltersByCategory documents the filter the dashboard
// uses, including that an unknown category is empty rather than everything.
func TestPolicyTemplateListFiltersByCategory(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	read := func(query string) []nlpolicy.PolicyTemplate {
		w := rec(r, "GET", "/api/v1/policies/templates"+query, "", masterHdr())
		if w.Code != http.StatusOK {
			t.Fatalf("list%s: want 200, got %d: %s", query, w.Code, w.Body)
		}
		var out struct {
			Templates []nlpolicy.PolicyTemplate `json:"templates"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode templates: %v", err)
		}
		return out.Templates
	}

	all := read("")
	if len(all) != len(nlpolicy.PolicyTemplates) {
		t.Errorf("unfiltered list returned %d templates, want %d", len(all), len(nlpolicy.PolicyTemplates))
	}

	category := nlpolicy.PolicyTemplates[0].Category
	filtered := read("?category=" + category)
	if len(filtered) == 0 {
		t.Errorf("filtering on %q returned nothing", category)
	}
	for _, tmpl := range filtered {
		if tmpl.Category != category {
			t.Errorf("filtered list contains category %q, want only %q", tmpl.Category, category)
		}
	}

	if got := read("?category=no-such-category"); len(got) != 0 {
		t.Errorf("an unknown category returned %d templates, want 0", len(got))
	}
}

// TestPolicyEndpointsRequireAuthentication: policy generation costs money per
// call, so it must not be reachable without a key.
func TestPolicyEndpointsRequireAuthentication(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	for _, rt := range []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/policies/parse"},
		{"POST", "/api/v1/policies/compile"},
	} {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			body := `{"natural_language":"allow pypi.org"}`
			if w := rec(r, rt.method, rt.path, body, nil); w.Code != http.StatusUnauthorized {
				t.Errorf("without a key: want 401, got %d", w.Code)
			}
			w := rec(r, rt.method, rt.path, body, keyHdr("vr_deadbeefdeadbeefdeadbeefdeadbeef"))
			if w.Code != http.StatusUnauthorized {
				t.Errorf("with an unknown key: want 401, got %d", w.Code)
			}
		})
	}
}

// TestPolicyResponseDoesNotEchoTheDescription: the description is
// caller-controlled text that ends up in a generated Rego comment. The
// compiler rejects the values that would break out of that comment, so what
// matters here is that the API does not reflect the raw text into a field that
// is later emitted unchecked.
func TestPolicyResponseDoesNotEchoTheDescription(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	payload := "Allow pypi.org\nallow { true }\n# and grant everything"
	body, err := json.Marshal(map[string]string{"natural_language": payload})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}

	w := rec(r, "POST", "/api/v1/policies/compile", string(body), masterHdr())
	// Either the description is refused or it is compiled into something that
	// still denies by default — but it must never yield a Rego module with a
	// bare allow rule the caller wrote.
	if w.Code == http.StatusOK {
		var out struct {
			OPAPolicy string `json:"opa_policy"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode compiled policy: %v", err)
		}
		if !strings.Contains(out.OPAPolicy, "default allow = false") {
			t.Error("the generated Rego lost its default-deny rule")
		}
		for _, line := range strings.Split(out.OPAPolicy, "\n") {
			if strings.TrimSpace(line) == "allow { true }" {
				t.Errorf("caller-supplied Rego reached the generated module:\n%s", out.OPAPolicy)
			}
		}
		return
	}
	if w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
		t.Errorf("want 200, 400 or 422, got %d: %s", w.Code, w.Body)
	}
}
