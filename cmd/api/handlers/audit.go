package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

type AuditHandler struct {
	h *Hub
}

func NewAuditHandler(h *Hub) *AuditHandler { return &AuditHandler{h: h} }

// parseAuditTime reads an optional RFC3339 timestamp or YYYY-MM-DD date query
// parameter. It writes the 400 response itself and reports false on bad input.
func parseAuditTime(c *gin.Context, name string) (*time.Time, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t, true
		}
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + name + ": use RFC3339 or YYYY-MM-DD"})
	return nil, false
}

// GET /api/v1/audit
// Query: session_id, action (prefix, e.g. "apikey."), from, to, seq, id,
// sort ("time", the default, or "seq" for chain order), page, limit.
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

	// C-2: Non-master callers only see their own audit logs, including when the
	// query is limited to a session. When a session_id filter is provided, use
	// the full org-aware access check (same logic as other session endpoints) to
	// decide whether the caller has at least viewer access. Org members who did
	// not create the session can open it, but still only see their own entries.
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
			slog.Error("failed to verify session", "err", err, "method", c.Request.Method, "path", c.FullPath())
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
	}

	filter := dbpkg.AuditFilter{SessionID: sessionIDPtr, Actor: auditActor, ActionPrefix: strings.TrimSpace(c.Query("action"))}
	var ok bool
	if filter.From, ok = parseAuditTime(c, "from"); !ok {
		return
	}
	if filter.To, ok = parseAuditTime(c, "to"); !ok {
		return
	}

	if raw := strings.TrimSpace(c.Query("seq")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid seq"})
			return
		}
		filter.Seq = &n
	}
	if raw := strings.TrimSpace(c.Query("id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		filter.ID = &id
	}
	switch c.Query("sort") {
	case "", "time":
	case "seq":
		filter.BySeq = true
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort: use time or seq"})
		return
	}

	logs, err := dbpkg.ListAuditLogsFiltered(c.Request.Context(), ah.h.db, filter, pg.limit, pg.offset)
	if err != nil {
		slog.Error("list audit logs", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit logs"})
		return
	}
	total, _ := dbpkg.CountAuditLogsFiltered(c.Request.Context(), ah.h.db, filter)
	actors := make([]string, 0, len(logs))
	for _, l := range logs {
		actors = append(actors, l.Actor)
	}
	if names, err := dbpkg.APIKeyNames(c.Request.Context(), ah.h.db, actors); err != nil {
		slog.Warn("resolve actor names", "err", err)
	} else {
		for _, l := range logs {
			l.ActorName = names[l.Actor]
		}
	}
	if ah.h.audit != nil && ah.h.audit.Enabled() {
		ah.h.audit.Classify(c.Request.Context(), logs)
	}
	c.JSON(http.StatusOK, gin.H{"audit_logs": logs, "pagination": pg.response(total)})
}

// GET /api/v1/audit/integrity
// Verifies the whole log (signatures, chain links, deleted entries, chain head)
// rather than one page, so a problem on page 2 is visible from page 1. The
// entry list and the missing sequence numbers are only shown to the master
// key; other keys get the same verdict with counts of their own and of other
// keys' entries.
func (ah *AuditHandler) Integrity(c *gin.Context) {
	if ah.h.audit == nil {
		c.JSON(http.StatusOK, gin.H{"status": audit.IntegrityDisabled, "problems": []string{}})
		return
	}
	res, err := ah.h.audit.Integrity(c.Request.Context(), c.Query("refresh") == "true")
	if err != nil {
		slog.Error("audit integrity check failed", "err", err, "method", c.Request.Method, "path", c.FullPath())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify the audit log"})
		return
	}
	if actor := middleware.Actor(c); actor != "master" {
		res = res.ScopedTo(actor)
	}
	c.JSON(http.StatusOK, res)
}
