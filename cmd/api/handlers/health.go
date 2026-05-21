package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	h *Hub
}

func NewHealthHandler(h *Hub) *HealthHandler { return &HealthHandler{h: h} }

// GET /health
// Returns a structured health object covering each major dependency.
// Responds 200 when all required checks pass, 503 when the DB is unreachable.
// Docker failures are reported but do not change the status code (the API can
// still serve non-run requests without a live Docker daemon).
func (hh *HealthHandler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	checks := gin.H{}
	healthy := true

	// ── Database ─────────────────────────────────────────────────────────────
	if err := hh.h.db.PingContext(ctx); err != nil {
		checks["database"] = gin.H{"status": "error", "error": err.Error()}
		healthy = false
	} else {
		checks["database"] = gin.H{"status": "ok"}
	}

	// ── Docker ───────────────────────────────────────────────────────────────
	// docker may be nil in test environments that don't mount a daemon socket.
	if hh.h.docker != nil {
		if _, err := hh.h.docker.Inner().Ping(ctx); err != nil {
			checks["docker"] = gin.H{"status": "error", "error": err.Error()}
			// Docker degraded is not fatal for the health endpoint — DB-only
			// requests (key management, audit, etc.) still work fine.
		} else {
			checks["docker"] = gin.H{"status": "ok"}
		}
	} else {
		checks["docker"] = gin.H{"status": "not_configured"}
	}

	// ── Job queue ─────────────────────────────────────────────────────────────
	if hh.h.queue != nil {
		checks["job_queue"] = gin.H{
			"status":  "ok",
			"pending": hh.h.queue.Len(),
		}
	}

	status := "ok"
	code := http.StatusOK
	if !healthy {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	c.JSON(code, gin.H{"status": status, "checks": checks})
}
