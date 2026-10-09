package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/internal/jev"
)

func TestJevGateDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jh := &JevHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/jev-gate", jh.Gate)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/jev-gate", strings.NewReader(`{"claim":"x","evidence":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestJevGatePass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("missing bearer")
		}
		_ = json.NewEncoder(w).Encode(jev.Response{
			Model: "jev-test",
			Answers: map[string]jev.Answer{
				"complete":        {Type: "noul", Noul: 0.95},
				"evidence_backed": {Type: "noul", Noul: 0.9},
			},
		})
	}))
	defer srv.Close()

	jh := &JevHandler{client: &jev.Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}}
	r := gin.New()
	r.POST("/api/v1/verify/jev-gate", func(c *gin.Context) {
		c.Set("actor", "tester")
		jh.Gate(c)
	})
	body := `{"claim":"Installed successfully","evidence":"{\"passed\":true}","min_noul":0.7}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/jev-gate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var res jev.GateResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if !res.Passed || res.Action != "pass" {
		t.Fatalf("%+v", res)
	}
}

func TestJevGateFailConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jev.Response{
			Answers: map[string]jev.Answer{
				"complete":        {Type: "noul", Noul: 0.1},
				"evidence_backed": {Type: "noul", Noul: 0.1},
			},
		})
	}))
	defer srv.Close()
	jh := &JevHandler{client: &jev.Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}}
	r := gin.New()
	r.POST("/g", jh.Gate)
	req := httptest.NewRequest(http.MethodPost, "/g", strings.NewReader(`{"claim":"done","evidence":"nope","on_fail":"fail"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", w.Code)
	}
}

func TestJevGateRejectsHugeClaim(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jh := &JevHandler{client: &jev.Client{APIKey: "k"}}
	r := gin.New()
	r.POST("/g", jh.Gate)
	claim := strings.Repeat("c", jev.MaxClaimRunes+1)
	body, _ := json.Marshal(map[string]any{"claim": claim, "evidence": "x"})
	req := httptest.NewRequest(http.MethodPost, "/g", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestJevVerifyClaimsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jh := &JevHandler{}
	r := gin.New()
	r.POST("/v", jh.VerifyClaims)
	req := httptest.NewRequest(http.MethodPost, "/v", strings.NewReader(`{"claim":"a","evidence":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d", w.Code)
	}
}
