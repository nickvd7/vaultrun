package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/internal/config"
)

func TestCreateSessionRejectsInvalidName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sh := NewSessionHandler(&Hub{cfg: &config.Config{}})
	for _, name := range []string{"bad name!!", "-leading", "a/b", strings.Repeat("x", 65), "semi;colon"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"name":"`+name+`"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		sh.Create(c)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid name") {
			t.Errorf("name %q: status=%d body=%s, want 400 invalid name", name, w.Code, w.Body.String())
		}
	}
}

func TestSessionNamePattern(t *testing.T) {
	for _, ok := range []string{"demo", "my session 2", "a.b_c-d", "7up"} {
		if !sessionNameRe.MatchString(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
}
