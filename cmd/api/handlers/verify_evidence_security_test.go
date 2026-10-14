package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/internal/verify"
)

// Security: tampering with exported evidence must fail integrity checks.
func TestEvidenceSecurityTamperFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := []byte("security-test-hmac-key-32bytes!!!")
	vh := &VerifyHandler{hmacKey: key}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence",
		strings.NewReader(`{"include_controls":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var rec verify.EvidenceRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}

	// Flip passed bit without updating digest.
	rec.Passed = !rec.Passed
	if err := verify.VerifyEvidenceIntegrity(&rec, key); err == nil {
		t.Fatal("tampered passed must fail digest")
	}

	// Restore and flip suite summary inside controls.
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	if rec.Controls != nil {
		rec.Controls.Summary = "forged"
		if err := verify.VerifyEvidenceIntegrity(&rec, key); err == nil {
			t.Fatal("tampered controls summary must fail digest")
		}
	}

	// Forged signature with valid digest.
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	rec.Sig = strings.Repeat("ab", 32)
	if err := verify.VerifyEvidenceIntegrity(&rec, key); err == nil {
		t.Fatal("forged sig must fail")
	}
}

// Security: empty / malformed JSON must not 500.
func TestEvidenceSecurityMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	for _, body := range []string{``, `{`, `null`, `[]`, `{"include_controls":"yes"}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("body %q → %d (want 4xx)", body, w.Code)
		}
	}
}

// Security: NUL / control bytes in observation must not appear in export.
func TestEvidenceSecurityStripsNULInHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	body := `{"spec":{"exit_code_zero":true},"observation":{"exit_code":0,"stdout":"ok\u0000secret"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "\\u0000") || strings.Contains(w.Body.String(), "\x00") {
		t.Fatal("NUL leaked into evidence JSON")
	}
	var rec verify.EvidenceRecord
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	if strings.Contains(rec.Checkpoint.Observation.Stdout, "\x00") {
		t.Fatal("NUL in observation")
	}
	if !strings.Contains(rec.Checkpoint.Observation.Stdout, "secret") {
		// After strip, "oksecret" remains — content preserved minus NUL.
		t.Fatalf("stdout=%q", rec.Checkpoint.Observation.Stdout)
	}
}

// Security: verification_id path without store must not panic / leak.
func TestEvidenceSecurityVerificationIDNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", func(c *gin.Context) {
		c.Set("actor", "master")
		vh.Evidence(c)
	})

	body := `{"verification_id":"11111111-1111-1111-1111-111111111111"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", w.Code, w.Body.String())
	}
}

// Security: unknown verification UUID returns 404, not 500, when store missing is already covered;
// invalid UUID in JSON should 400.
func TestEvidenceSecurityBadVerificationUUID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	body := `{"verification_id":"not-uuid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// Security: sign=true without configured key must not invent a signature.
func TestEvidenceSecurityNoKeyNoSig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{} // no hmacKey
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	body := `{"include_controls":true,"sign":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var rec verify.EvidenceRecord
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	if rec.Sig != "" {
		t.Fatal("must not sign without key")
	}
	if err := verify.VerifyEvidenceIntegrity(&rec, nil); err != nil {
		t.Fatal(err)
	}
}

// Security: actor bound into subject must come from auth context, not request body.
func TestEvidenceSecurityActorFromContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", func(c *gin.Context) {
		c.Set("actor", "trusted-actor")
		vh.Evidence(c)
	})

	// Attempt to smuggle actor via unused fields — only step_name is accepted from body.
	body := `{"include_controls":true,"step_name":"legit","actor":"evil"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var rec verify.EvidenceRecord
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	if rec.Subject.Actor != "trusted-actor" {
		t.Fatalf("actor=%q (client must not forge)", rec.Subject.Actor)
	}
}

// Security: unauthorized verification reads must not leak "session not found".
func TestEvidenceSecurityUniformVerification404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{h: &Hub{}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("actor", "outsider")
	ok := vh.authorizeVerificationRead(c, &verify.Record{})
	if ok || w.Code != http.StatusNotFound {
		t.Fatalf("ok=%v code=%d body=%s", ok, w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "verification not found") {
		t.Fatalf("body=%s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "session not found") {
		t.Fatal("must not leak session not found")
	}
}

func TestEvidenceSecuritySessionLessDenyMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("actor", "not-master")
	if vh.authorizeVerificationRead(c, &verify.Record{SessionID: nil}) {
		t.Fatal("expected deny")
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "verification not found" {
		t.Fatalf("error=%q", body["error"])
	}
}
