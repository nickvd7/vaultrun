package middleware

import (
	"bytes"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// maxLoggedBody caps how much of an error response is copied into the log.
const maxLoggedBody = 512

type captureWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if w.Status() >= http.StatusInternalServerError && w.body.Len() < maxLoggedBody {
		room := maxLoggedBody - w.body.Len()
		if len(b) < room {
			room = len(b)
		}
		w.body.Write(b[:room])
	}
	return w.ResponseWriter.Write(b)
}

// ServerErrorLog logs every 5xx response with its route and the error text the
// client received. Handlers should log the underlying cause themselves; this is
// the backstop that makes sure a 500 never passes without a trace in the API
// log, including from handlers that forgot to.
func ServerErrorLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		w := &captureWriter{ResponseWriter: c.Writer}
		c.Writer = w
		c.Next()

		if status := c.Writer.Status(); status >= http.StatusInternalServerError {
			slog.Error("request failed",
				"status", status,
				"method", c.Request.Method,
				"route", c.FullPath(),
				"response", w.body.String(),
			)
		}
	}
}
