package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/internal/version"
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
	// Len() returns -1 when the backend (Redis) is unreachable. Surface this
	// as a degraded status so operators can detect Redis failures via health checks.
	if hh.h.queue != nil {
		qLen := hh.h.queue.Len()
		if qLen < 0 {
			checks["job_queue"] = gin.H{
				"status": "error",
				"error":  "queue backend unreachable (check Redis connectivity)",
			}
			healthy = false
		} else {
			checks["job_queue"] = gin.H{
				"status":  "ok",
				"pending": qLen,
			}
		}
	}

	status := "ok"
	code := http.StatusOK
	if !healthy {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	resp := gin.H{"status": status, "version": version.Version(), "edition": version.Edition, "checks": checks}
	if hh.h.cfg.MultiRegion.Region != "" {
		resp["region"] = hh.h.cfg.MultiRegion.Region
	}
	c.JSON(code, resp)
}

// GET /api/v1/info
// What this server offers, for the dashboard: release and edition, the OCI
// runtime sandboxes run under, and whether evidence packs and external
// timestamps are configured. Any authenticated key may read it.
func (hh *HealthHandler) Info(c *gin.Context) {
	runtime := ""
	if hh.h.docker != nil {
		runtime = hh.h.docker.Runtime()
	}
	c.JSON(http.StatusOK, gin.H{
		"version":        version.Version(),
		"edition":        version.Edition,
		"edition_detail": version.EditionDetail,
		"sandbox": gin.H{
			"runtime": runtime,
			"gvisor":  runtime == "runsc" || strings.HasPrefix(runtime, "runsc-"),
		},
		"evidence": gin.H{
			"enabled":     hh.h.evidenceSigner != nil,
			"timestamped": hh.h.evidenceTSAURL != "",
		},
	})
}
