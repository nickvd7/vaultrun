package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestServerErrorLogRecordsOnly5xx(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	r := gin.New()
	r.Use(ServerErrorLog())
	r.POST("/boom", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "workspace init failed"})
	})
	r.GET("/missing", func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "nope"})
	})

	for _, tc := range []struct{ method, path string }{{"POST", "/boom"}, {"GET", "/missing"}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
	}

	out := buf.String()
	if !strings.Contains(out, "request failed") || !strings.Contains(out, "/boom") || !strings.Contains(out, "workspace init failed") {
		t.Errorf("500 not logged with route and message:\n%s", out)
	}
	if strings.Contains(out, "/missing") {
		t.Errorf("404 must not be logged as a server error:\n%s", out)
	}
}
