package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
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

	// C-2: Non-master callers only see their own audit logs.
	// When a session_id filter is provided, verify the caller owns that session
	// before listing — prevents cross-tenant audit log enumeration via UUID guessing.
	actor := middleware.Actor(c)
	if actor != "master" && sessionIDPtr != nil {
		session, err := dbpkg.GetSession(c.Request.Context(), ah.h.db, *sessionIDPtr)
		if err == sql.ErrNoRows || (err == nil && session.CreatedBy != actor) {
			// Return an empty list (not 404) so the caller cannot use error
			// shape to confirm whether the session UUID exists at all.
			c.JSON(http.StatusOK, gin.H{"audit_logs": []any{}, "pagination": pg})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify session"})
			return
		}
	}

	// Master gets an empty string which means "all actors" in the query.
	auditActor := actor
	if actor == "master" {
		auditActor = ""
	}

	logs, err := dbpkg.ListAuditLogs(c.Request.Context(), ah.h.db, sessionIDPtr, auditActor, pg.limit, pg.offset)
	if err != nil {
		slog.Error("list audit logs", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit logs"})
		return
	}
	total, _ := dbpkg.CountAuditLogs(c.Request.Context(), ah.h.db, sessionIDPtr, auditActor)
	c.JSON(http.StatusOK, gin.H{"audit_logs": logs, "pagination": pg.response(total)})
}
