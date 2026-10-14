package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/verify"
)

func TestEvidenceControlsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{hmacKey: []byte("test-evidence-hmac-key-32bytes!!")}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", func(c *gin.Context) {
		c.Set("actor", "master")
		vh.Evidence(c)
	})

	body := `{"include_controls":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var rec verify.EvidenceRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Kind != verify.EvidenceKindControls || rec.Schema != verify.EvidenceSchema {
		t.Fatalf("%+v", rec)
	}
	if rec.ContentDigest == "" || rec.Sig == "" {
		t.Fatalf("missing seal: digest=%q sig=%q", rec.ContentDigest, rec.Sig)
	}
	if err := verify.VerifyEvidenceIntegrity(&rec, vh.hmacKey); err != nil {
		t.Fatal(err)
	}
	if !rec.Controls.PipelineDiscriminates {
		t.Fatal("controls should discriminate")
	}
}

func TestEvidenceInlineCheckpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", func(c *gin.Context) {
		c.Set("actor", "tester")
		vh.Evidence(c)
	})

	body := `{
		"spec": {"exit_code_zero": true, "stdout_contains": "Successfully"},
		"observation": {"exit_code": 0, "stdout": "Successfully installed"},
		"step_name": "install"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var rec verify.EvidenceRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Kind != verify.EvidenceKindCheckpoint || !rec.Passed {
		t.Fatalf("%+v", rec)
	}
	if rec.Sig != "" {
		t.Fatal("sig should be empty without HMAC key")
	}
	if rec.Subject.Actor != "tester" || rec.Subject.StepName != "install" {
		t.Fatalf("subject: %+v", rec.Subject)
	}
}

func TestEvidenceRequiresSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEvidenceRejectsHugeStdout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	huge := strings.Repeat("A", verifyMaxStdoutBytes+1)
	payload, _ := json.Marshal(map[string]any{
		"spec":        map[string]any{"exit_code_zero": true},
		"observation": map[string]any{"exit_code": 0, "stdout": huge},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestEvidenceRejectsLongStepName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	payload, _ := json.Marshal(map[string]any{
		"include_controls": true,
		"step_name":        strings.Repeat("x", verifyMaxStepName+1),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestEvidenceSignFalseSkipsHMAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{hmacKey: []byte("present-but-opted-out!!!!!!!!!")}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	body := `{"include_controls":true,"sign":false}`
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
		t.Fatal("expected empty sig when sign=false")
	}
	if rec.ContentDigest == "" {
		t.Fatal("digest still required")
	}
}

func TestEvidenceEmptySpecRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	body := `{"spec":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestEvidenceFileExistsRequiresSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	body := `{"spec":{"file_exists":"out.txt"},"observation":{"exit_code":0}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetEvidenceUnknownID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{} // store nil → 503
	r := gin.New()
	r.GET("/api/v1/verifications/:id/evidence", vh.GetEvidence)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/verifications/"+id.String()+"/evidence", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestGetEvidenceInvalidUUID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.GET("/api/v1/verifications/:id/evidence", vh.GetEvidence)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/verifications/not-a-uuid/evidence", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Fatalf("want 400/404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthorizeVerificationReadNoSessionNonMaster(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("actor", "some-key")

	ok := vh.authorizeVerificationRead(c, &verify.Record{})
	if ok {
		t.Fatal("non-master must not read verification without session")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestAuthorizeVerificationReadNoSessionMaster(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("actor", "master")

	if !vh.authorizeVerificationRead(c, &verify.Record{}) {
		t.Fatal("master should read session-less verification")
	}
}

func TestPopulateEvidenceFromRecord(t *testing.T) {
	want := true
	zero := 0
	spec, _ := json.Marshal(verify.Spec{ExitCodeZero: &want})
	obs, _ := json.Marshal(verify.Observation{ExitCode: &zero, Stdout: "ok"})
	checks, _ := json.Marshal([]verify.Check{{Name: "exit_code_zero", Passed: true}})
	sid := uuid.New()
	rec := &verify.Record{
		ID: uuid.New(), SessionID: &sid, Spec: spec, Observation: obs,
		Checks: checks, Passed: true, StepName: "s",
	}
	in := &verify.EvidenceInput{}
	if err := populateEvidenceFromRecord(in, rec); err != nil {
		t.Fatal(err)
	}
	if !in.HasCheckpoint || !in.Result.Passed || in.SessionID == nil {
		t.Fatalf("%+v", in)
	}
}

func TestEvidenceCombinedKind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{hmacKey: []byte("k")}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", func(c *gin.Context) {
		c.Set("actor", "a")
		vh.Evidence(c)
	})

	body := `{
		"spec": {"exit_code_zero": true},
		"observation": {"exit_code": 0},
		"include_controls": true
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var rec verify.EvidenceRecord
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	if rec.Kind != verify.EvidenceKindCombined {
		t.Fatalf("kind=%s", rec.Kind)
	}
}

func TestEvidenceFailedCheckpointStillExports(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.POST("/api/v1/verify/evidence", vh.Evidence)

	body := `{
		"spec": {"exit_code_zero": true, "stdout_contains": "Successfully"},
		"observation": {"exit_code": 1, "stdout": "error"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify/evidence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var rec verify.EvidenceRecord
	_ = json.Unmarshal(w.Body.Bytes(), &rec)
	if rec.Passed {
		t.Fatal("expected passed=false")
	}
	if rec.ContentDigest == "" {
		t.Fatal("failed evidence must still be sealed")
	}
}
