package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	h *Hub
}

func NewHealthHandler(h *Hub) *HealthHandler { return &HealthHandler{h: h} }

// GET /health
func (hh *HealthHandler) Health(c *gin.Context) {
	ctx := c.Request.Context()

	dbOK := hh.h.db.PingContext(ctx) == nil

	status := "ok"
	code := http.StatusOK
	if !dbOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	c.JSON(code, gin.H{
		"status":    status,
		"time":      time.Now().UTC(),
		"db":        boolStatus(dbOK),
	})
}

func boolStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}
