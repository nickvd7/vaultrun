package jev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSanitizeAndRedact(t *testing.T) {
	in := "key vr_abcdefghijklmnopqrstuvwxyz012345 and sk-abcdefghijklmnopqrstuvwxyz012345 and ts_abcdefghijklmnopqrstuvwxyz012345\x00"
	out := SanitizeState(in)
	if strings.Contains(out, "vr_abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("vr_ token not redacted")
	}
	if strings.Contains(out, "\x00") {
		t.Fatal("nul not stripped")
	}
	if !strings.Contains(out, "vr_[REDACTED]") {
		t.Fatalf("got %q", out)
	}
}

func TestSanitizeTruncates(t *testing.T) {
	huge := strings.Repeat("A", MaxStateBytes+100)
	out := SanitizeState(huge)
	if len(out) > MaxStateBytes+32 {
		t.Fatalf("len=%d", len(out))
	}
}

func TestValidateClaim(t *testing.T) {
	if err := ValidateClaim(""); err == nil {
		t.Fatal("empty")
	}
	if err := ValidateClaim(strings.Repeat("x", MaxClaimRunes+1)); err == nil {
		t.Fatal("too long")
	}
	if err := ValidateClaim("done"); err != nil {
		t.Fatal(err)
	}
}

func TestParseOnFail(t *testing.T) {
	if ParseOnFail("hold") != OnFailHold {
		t.Fatal("hold")
	}
	if ParseOnFail("fail") != OnFailFail {
		t.Fatal("fail")
	}
}

func TestConfigFromEnvRequiresKeyAndHTTPS(t *testing.T) {
	_, err := ConfigFromEnv(func(string) string { return "" })
	if err == nil {
		t.Fatal("expected missing key")
	}
	_, err = ConfigFromEnv(func(k string) string {
		switch k {
		case "TYPESAFE_API_KEY":
			return "ts_testkey"
		case "TYPESAFE_BASE_URL":
			return "http://127.0.0.1:9"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected reject private/http base URL")
	}
	c, err := ConfigFromEnv(func(k string) string {
		if k == "TYPESAFE_API_KEY" {
			return "ts_testkey"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != ProviderTypeSafe || c.BaseURL != TypeSafeBaseURL || c.Model != TypeSafeModel {
		t.Fatalf("%+v", c)
	}
}

func TestConfigFromEnvOpenJEV(t *testing.T) {
	c, err := ConfigFromEnv(func(k string) string {
		switch k {
		case "OPENJEV_API_KEY":
			return "oj_testkey_abcdefghijklmnopqrstuvwxyz"
		case "JEV_PROVIDER":
			return "openjev"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != ProviderOpenJEV {
		t.Fatalf("provider=%s", c.Provider)
	}
	if c.BaseURL != OpenJEVBaseURL {
		t.Fatalf("base=%s", c.BaseURL)
	}
	if c.Model != OpenJEVModel {
		t.Fatalf("model=%s", c.Model)
	}
}

func TestConfigFromEnvOpenJEVAutoDetect(t *testing.T) {
	c, err := ConfigFromEnv(func(k string) string {
		if k == "OPENJEV_API_KEY" {
			return "oj_only_key_abcdefghijklmnop"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != ProviderOpenJEV || c.BaseURL != OpenJEVBaseURL {
		t.Fatalf("%+v", c)
	}
}

func TestResolveProvider(t *testing.T) {
	if ResolveProvider(func(string) string { return "" }) != ProviderTypeSafe {
		t.Fatal("default typesafe")
	}
	if ResolveProvider(func(k string) string {
		if k == "JEV_PROVIDER" {
			return "openjev"
		}
		return ""
	}) != ProviderOpenJEV {
		t.Fatal("explicit openjev")
	}
}

func TestSanitizeRedactsOpenJEVKey(t *testing.T) {
	in := "oj_abcdefghijklmnopqrstuvwxyz012345"
	out := SanitizeState(in)
	if strings.Contains(out, "oj_abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("oj_ key not redacted")
	}
}

func TestGateClaimPassAndHold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("auth %q", r.Header.Get("Authorization"))
		}
		var req Request
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if strings.Contains(string(body), "vr_abcdefghijklmnopqrstuvwxyz0123456789") {
			t.Fatal("raw API key leaked to Jev")
		}
		_ = json.NewEncoder(w).Encode(Response{
			Model: "jev-test",
			Answers: map[string]Answer{
				"complete":        {Type: "noul", Noul: 0.9},
				"evidence_backed": {Type: "noul", Noul: 0.85},
			},
		})
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "test-key", Model: "jev-latest", HTTP: srv.Client()}
	res, err := c.GateClaim(context.Background(), GateInput{
		Claim:    "Installed package successfully",
		Evidence: `{"passed":true,"secret":"vr_abcdefghijklmnopqrstuvwxyz0123456789"}`,
		MinNoul:  0.7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Action != "pass" {
		t.Fatalf("%+v", res)
	}

	// Low noul → hold
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Response{
			Answers: map[string]Answer{
				"complete":        {Type: "noul", Noul: 0.2},
				"evidence_backed": {Type: "noul", Noul: 0.9},
			},
		})
	}))
	defer srv2.Close()
	c2 := &Client{BaseURL: srv2.URL, APIKey: "k", HTTP: srv2.Client()}
	res2, err := c2.GateClaim(context.Background(), GateInput{
		Claim: "done", Evidence: "ok", MinNoul: 0.8, OnFail: OnFailHold,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Passed || res2.Action != "hold" {
		t.Fatalf("%+v", res2)
	}
}

func TestVerifyClaimsBound(t *testing.T) {
	c := &Client{APIKey: "k"}
	_, err := c.VerifyClaims(context.Background(), "e", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	claims := make([]string, 21)
	for i := range claims {
		claims[i] = "c"
	}
	_, err = c.VerifyClaims(context.Background(), "e", claims)
	if err == nil {
		t.Fatal("expected too many")
	}
}

func TestVerifyClaimsRedactsStateClaims(t *testing.T) {
	var saw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		saw = string(b)
		_ = json.NewEncoder(w).Encode(Response{
			Answers: map[string]Answer{
				"claim_0": {Type: "choice", Choice: "supports", Confidence: 0.9},
			},
		})
	}))
	defer srv.Close()
	secret := "vr_abcdefghijklmnopqrstuvwxyz0123456789abcd"
	c := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	_, err := c.VerifyClaims(context.Background(), "evidence ok", []string{"token " + secret})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saw, secret) {
		t.Fatal("raw claim secret leaked in state.claims")
	}
	if !strings.Contains(saw, "vr_[REDACTED]") {
		t.Fatalf("expected redaction: %s", saw)
	}
}

func TestEvaluateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	_, err := c.Evaluate(context.Background(), "s", map[string]Question{
		"q": {Type: "noul", Instructions: "yes?"},
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err=%v", err)
	}
}
