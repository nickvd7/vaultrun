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

// Security: request evidence size capped before outbound call.
func TestJevSecurityEvidenceTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jh := &JevHandler{client: &jev.Client{APIKey: "k"}}
	r := gin.New()
	r.POST("/g", jh.Gate)
	huge := strings.Repeat("E", jev.MaxStateBytes*2+1)
	body, _ := json.Marshal(map[string]any{"claim": "x", "evidence": huge})
	req := httptest.NewRequest(http.MethodPost, "/g", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// Security: malformed JSON must not 500.
func TestJevSecurityMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jh := &JevHandler{client: &jev.Client{APIKey: "k"}}
	r := gin.New()
	r.POST("/g", jh.Gate)
	for _, body := range []string{``, `{`, `null`, `[]`} {
		req := httptest.NewRequest(http.MethodPost, "/g", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("body %q → %d", body, w.Code)
		}
	}
}

// Security: secrets in evidence are redacted before leaving the host.
func TestJevSecurityRedactsOutboundSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var saw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		saw = string(buf)
		_ = json.NewEncoder(w).Encode(jev.Response{
			Answers: map[string]jev.Answer{
				"complete":        {Type: "noul", Noul: 0.9},
				"evidence_backed": {Type: "noul", Noul: 0.9},
			},
		})
	}))
	defer srv.Close()

	jh := &JevHandler{client: &jev.Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}}
	r := gin.New()
	r.POST("/g", jh.Gate)
	secret := "vr_abcdefghijklmnopqrstuvwxyz0123456789abcd"
	body := `{"claim":"ok","evidence":"token ` + secret + `"}`
	req := httptest.NewRequest(http.MethodPost, "/g", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(saw, secret) {
		t.Fatal("raw API key leaked to Jev upstream")
	}
	if !strings.Contains(saw, "vr_[REDACTED]") {
		t.Fatalf("expected redaction marker in outbound body: %s", saw)
	}
}
