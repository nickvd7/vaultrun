package handlers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
)

type AuditHandler struct {
	h *Hub
}

func NewAuditHandler(h *Hub) *AuditHandler { return &AuditHandler{h: h} }

// GET /api/v1/audit
func (ah *AuditHandler) List(c *gin.Context) {
	pg := pagination(c)

	var sessionIDPtr *uuid.UUID
	if sid := c.Query("session_id"); sid != "" {
		id, err := uuid.Parse(sid)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
			return
		}
		sessionIDPtr = &id
	}

	logs, err := dbpkg.ListAuditLogs(c.Request.Context(), ah.h.db, sessionIDPtr, pg.limit, pg.offset)
	if err != nil {
		slog.Error("list audit logs", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit logs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"audit_logs": logs, "pagination": pg})
}
