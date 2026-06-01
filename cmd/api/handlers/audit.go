package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
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
	// When a session_id filter is provided, use the full org-aware access check
	// (same logic as other session endpoints) to determine whether the caller
	// has at least viewer access. This correctly handles org members who are not
	// the session creator but do have read access via org membership.
	actor := middleware.Actor(c)
	auditActor := actor
	if actor == "master" {
		auditActor = "" // empty = all actors in the DB query
	}
	if actor != "master" && sessionIDPtr != nil {
		// checkSessionAccess writes a 404 if access is denied; we want an empty list
		// instead to avoid leaking session existence, so we call it on a throwaway
		// response writer substitute — actually just replicate the logic inline.
		session, err := dbpkg.GetSession(c.Request.Context(), ah.h.db, *sessionIDPtr)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusOK, gin.H{"audit_logs": []any{}, "pagination": pg})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify session"})
			return
		}
		hasAccess := session.CreatedBy == actor
		if !hasAccess && session.OrgID != nil {
			role, roleErr := dbpkg.GetOrgMemberRole(c.Request.Context(), ah.h.db, *session.OrgID, actor)
			if roleErr == nil && models.RoleAtLeast(role, models.OrgRoleViewer) {
				hasAccess = true
			}
		}
		if !hasAccess {
			// Return empty list rather than 404 to avoid leaking session existence.
			c.JSON(http.StatusOK, gin.H{"audit_logs": []any{}, "pagination": pg})
			return
		}
		// For sessions the actor has access to (but did not create), show all
		// audit events for that session rather than filtering by actor.
		if session.CreatedBy != actor {
			auditActor = ""
		}
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
