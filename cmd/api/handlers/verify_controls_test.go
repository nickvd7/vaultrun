package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/internal/verify"
)

func TestVerifyControlsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	vh := &VerifyHandler{}
	r := gin.New()
	r.GET("/api/v1/verify/controls", vh.Controls)
	r.POST("/api/v1/verify/controls", vh.Controls)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/api/v1/verify/controls", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status %d: %s", method, w.Code, w.Body.String())
		}
		var rep verify.ControlReport
		if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		if !rep.PipelineDiscriminates || rep.SuiteID != verify.SuiteID {
			t.Fatalf("%s bad report: %+v", method, rep)
		}
		if rep.Fingerprint != verify.SuiteFingerprint() {
			t.Fatalf("fingerprint mismatch")
		}
	}
}
