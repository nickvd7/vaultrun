package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	h *Hub
}

func NewHealthHandler(h *Hub) *HealthHandler { return &HealthHandler{h: h} }

// GET /health — minimal response; no internal component status exposed to
// unauthenticated callers. Liveness only: the process is up and the DB is reachable.
func (hh *HealthHandler) Health(c *gin.Context) {
	ctx := c.Request.Context()

	if err := hh.h.db.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
