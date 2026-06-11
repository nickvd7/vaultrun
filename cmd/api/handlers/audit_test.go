package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/internal/config"
)

func newAuditTestContext(target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c, w
}

// TestAuditListInvalidSessionIDRejectedBeforeDBAccess verifies that a
// malformed session_id query parameter is rejected with 400 before any
// database lookup is attempted — this path is reachable without a live DB
// and without a configured actor.
func TestAuditListInvalidSessionIDRejectedBeforeDBAccess(t *testing.T) {
	h := &Hub{cfg: &config.Config{}}
	ah := NewAuditHandler(h)

	c, w := newAuditTestContext("/api/v1/audit?session_id=not-a-uuid")
	ah.List(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"invalid session_id"`) {
		t.Errorf("body = %q, want it to contain invalid session_id error", w.Body.String())
	}
}

func TestAuditListInvalidSessionIDRejectedForMasterActorToo(t *testing.T) {
	h := &Hub{cfg: &config.Config{}}
	ah := NewAuditHandler(h)

	c, w := newAuditTestContext("/api/v1/audit?session_id=still-not-a-uuid")
	c.Set("actor", "master")
	ah.List(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
