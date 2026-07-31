package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/templates"
)

// TemplateHandler handles template-related HTTP requests
type TemplateHandler struct {
	manager *templates.Manager
	hub     *Hub
}

// NewTemplateHandler creates a new template handler
func NewTemplateHandler(manager *templates.Manager, hub *Hub) *TemplateHandler {
	return &TemplateHandler{
		manager: manager,
		hub:     hub,
	}
}

// callerOrg returns the org a caller acts on behalf of, or nil when it belongs
// to none (the master key, or a key that was never added to an org).
//
// A principal can be a member of several orgs; the first row is used, matching
// the behaviour of the rest of the API where org selection is explicit only on
// session creation.
func (h *TemplateHandler) callerOrg(c *gin.Context, actor string) (*uuid.UUID, error) {
	if actor == "master" {
		return nil, nil
	}
	var orgID uuid.UUID
	err := h.hub.db.GetContext(c.Request.Context(), &orgID,
		"SELECT org_id FROM org_members WHERE principal = $1 ORDER BY joined_at LIMIT 1", actor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &orgID, nil
}

// mayRead reports whether the caller may see a template.
//
// Published templates are the marketplace and readable by every authenticated
// caller. An unpublished template is a draft: its image, env keys and startup
// script are only visible to the org that authored it. Get-by-id and
// get-by-slug have to enforce this themselves — the list endpoint's
// published-only filter does not apply to a direct lookup.
func (h *TemplateHandler) mayRead(c *gin.Context, actor string, tmpl *templates.Template) bool {
	if actor == "master" || tmpl.Published {
		return true
	}
	if tmpl.AuthorOrg == nil {
		return false
	}
	_, err := dbpkg.GetOrgMemberRole(c.Request.Context(), h.hub.db, *tmpl.AuthorOrg, actor)
	return err == nil
}

// mayAdminister reports whether the caller may modify or delete a template.
//
// Templates are shared infrastructure: whoever can rewrite one decides which
// image every future session built from it runs. Only an admin of the authoring
// org qualifies, and built-in templates (no authoring org) are master-key only.
func (h *TemplateHandler) mayAdminister(c *gin.Context, actor string, tmpl *templates.Template) bool {
	if actor == "master" {
		return true
	}
	if tmpl.AuthorOrg == nil {
		return false
	}
	role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), h.hub.db, *tmpl.AuthorOrg, actor)
	return err == nil && models.RoleAtLeast(role, models.OrgRoleAdmin)
}

// ListTemplates returns all templates with optional filtering
// GET /api/v1/templates
func (h *TemplateHandler) ListTemplates(c *gin.Context) {
	var filter templates.TemplateFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default to published templates unless user is admin
	actor := middleware.Actor(c)
	if actor != "master" && !filter.Published {
		filter.Published = true
	}

	tmplList, err := h.manager.List(c.Request.Context(), filter)
	if err != nil {
		slog.Error("list templates", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list templates"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"templates": tmplList})
}

// GetTemplate returns a single template
// GET /api/v1/templates/:id
func (h *TemplateHandler) GetTemplate(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template ID"})
		return
	}

	tmpl, err := h.manager.Get(c.Request.Context(), id)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	// Report a hidden draft as missing rather than forbidden: a 403 would
	// confirm the slug exists.
	if !h.mayRead(c, middleware.Actor(c), tmpl) {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	c.JSON(http.StatusOK, tmpl)
}

// GetTemplateBySlug returns a template by slug
// GET /api/v1/templates/slug/:slug
func (h *TemplateHandler) GetTemplateBySlug(c *gin.Context) {
	slug := c.Param("slug")

	tmpl, err := h.manager.GetBySlug(c.Request.Context(), slug)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	if !h.mayRead(c, middleware.Actor(c), tmpl) {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	c.JSON(http.StatusOK, tmpl)
}

// CreateTemplate creates a new custom template
// POST /api/v1/templates
func (h *TemplateHandler) CreateTemplate(c *gin.Context) {
	var req templates.CreateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	actor := middleware.Actor(c)
	orgID, err := h.callerOrg(c, actor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get organization"})
		return
	}

	tmpl, err := h.manager.Create(c.Request.Context(), orgID, req)
	if err == templates.ErrTemplateSlugExists {
		c.JSON(http.StatusConflict, gin.H{"error": "template with this slug already exists"})
		return
	}
	if errors.Is(err, templates.ErrInvalidTemplate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		slog.Error("persist template", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save template"})
		return
	}

	// Audit log
	h.hub.audit.Log(c.Request.Context(), audit.Event{
		Actor:  actor,
		Action: models.ActionTemplateCreated,
		Metadata: map[string]interface{}{
			"template_id": tmpl.ID.String(),
			"slug":        tmpl.Slug,
			"name":        tmpl.Name,
		},
	})

	c.JSON(http.StatusCreated, tmpl)
}

// UpdateTemplate updates a template
// PUT /api/v1/templates/:id
func (h *TemplateHandler) UpdateTemplate(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template ID"})
		return
	}

	var req templates.UpdateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	actor := middleware.Actor(c)

	existing, err := h.manager.Get(c.Request.Context(), id)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}
	if !h.mayAdminister(c, actor, existing) {
		c.JSON(http.StatusForbidden, gin.H{"error": "template is owned by another organization"})
		return
	}

	tmpl, err := h.manager.Update(c.Request.Context(), id, req)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if errors.Is(err, templates.ErrInvalidTemplate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		slog.Error("persist template", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save template"})
		return
	}

	// Audit log
	h.hub.audit.Log(c.Request.Context(), audit.Event{
		Actor:  actor,
		Action: models.ActionTemplateUpdated,
		Metadata: map[string]interface{}{
			"template_id": id.String(),
		},
	})

	c.JSON(http.StatusOK, tmpl)
}

// DeleteTemplate deletes a template
// DELETE /api/v1/templates/:id
func (h *TemplateHandler) DeleteTemplate(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template ID"})
		return
	}

	actor := middleware.Actor(c)

	existing, err := h.manager.Get(c.Request.Context(), id)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}
	if !h.mayAdminister(c, actor, existing) {
		c.JSON(http.StatusForbidden, gin.H{"error": "template is owned by another organization"})
		return
	}

	err = h.manager.Delete(c.Request.Context(), id)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}

	// Audit log
	h.hub.audit.Log(c.Request.Context(), audit.Event{
		Actor:  actor,
		Action: models.ActionTemplateDeleted,
		Metadata: map[string]interface{}{
			"template_id": id.String(),
		},
	})

	c.JSON(http.StatusOK, gin.H{"message": "template deleted"})
}

// enforceSessionPolicy applies the deployment's session gates to a template's
// configuration. It writes the error response itself and returns a non-nil
// error when the caller must be refused.
func (h *TemplateHandler) enforceSessionPolicy(c *gin.Context, actor string, tmpl *templates.Template) error {
	limits := h.hub.cfg.SessionLimits()

	if tmpl.Resources.CPULimit > limits.MaxCPU {
		err := fmt.Errorf("template cpu_limit %.1f exceeds maximum of %.1f", tmpl.Resources.CPULimit, limits.MaxCPU)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}
	if tmpl.Resources.MemoryLimitMB > limits.MaxMemoryMB {
		err := fmt.Errorf("template memory_limit_mb %d exceeds maximum of %d", tmpl.Resources.MemoryLimitMB, limits.MaxMemoryMB)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}
	if tmpl.Resources.TimeoutSeconds > limits.MaxTimeoutSec {
		err := fmt.Errorf("template timeout_seconds %d exceeds maximum of %d", tmpl.Resources.TimeoutSeconds, limits.MaxTimeoutSec)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}

	if !h.hub.cfg.ImageAllowed(tmpl.Image) {
		err := errors.New("template image not permitted")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}

	if limits.MaxSessionsPerActor > 0 && actor != "master" {
		count, cerr := dbpkg.CountActiveSessionsByActor(c.Request.Context(), h.hub.db, actor)
		if cerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count sessions"})
			return cerr
		}
		if count >= limits.MaxSessionsPerActor {
			err := fmt.Errorf("active session limit of %d reached", limits.MaxSessionsPerActor)
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return err
		}
	}

	return nil
}

// CreateSessionFromTemplate creates a new session from a template
// POST /api/v1/templates/:id/use
func (h *TemplateHandler) CreateSessionFromTemplate(c *gin.Context) {
	idStr := c.Param("id")
	templateID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template ID"})
		return
	}

	actor := middleware.Actor(c)

	// Get template
	tmpl, err := h.manager.Get(c.Request.Context(), templateID)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	if !h.mayRead(c, actor, tmpl) {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	// A template is a session request like any other, so it passes the same
	// gates as POST /sessions. Skipping them would make this endpoint a way to
	// run a disallowed image, claim more CPU or memory than the deployment
	// permits, or exceed the per-actor session quota.
	if err := h.enforceSessionPolicy(c, actor, tmpl); err != nil {
		return
	}

	orgID, err := h.callerOrg(c, actor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get organization"})
		return
	}

	// Parse optional override parameters
	var override struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&override)

	sessionName := override.Name
	if sessionName == "" {
		sessionName = "Session from " + tmpl.Name
	}

	// Create session with template configuration
	sessionID := uuid.New()
	wspath, err := h.hub.ws.Create(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "workspace creation failed"})
		return
	}

	now := time.Now().UTC()
	session := &models.Session{
		ID:             sessionID,
		Name:           &sessionName,
		Image:          tmpl.Image,
		Status:         models.SessionStatusCreated,
		NetworkEnabled: tmpl.Network.Enabled,
		CPULimit:       tmpl.Resources.CPULimit,
		MemoryLimitMB:  tmpl.Resources.MemoryLimitMB,
		TimeoutSeconds: tmpl.Resources.TimeoutSeconds,
		WorkspacePath:  wspath,
		Labels:         models.JSONB{},
		AllowedHosts:   models.StringArray(tmpl.Network.AllowedHosts),
		CreatedBy:      actor,
		OrgID:          orgID,
		TemplateID:     &templateID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := dbpkg.CreateSession(c.Request.Context(), h.hub.db, session); err != nil {
		_ = h.hub.ws.Delete(sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "persist session failed"})
		return
	}

	// Link template to session
	_, err = h.hub.db.ExecContext(c.Request.Context(),
		"UPDATE sessions SET template_id = $1 WHERE id = $2",
		templateID, sessionID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to link template"})
		return
	}

	// Usage feeds the marketplace's use_count; a failure here must not fail the
	// session the caller actually asked for.
	if err := h.manager.RecordUsage(c.Request.Context(), templateID, sessionID, orgID); err != nil {
		slog.Warn("record template usage", "err", err, "template_id", templateID, "session_id", sessionID)
	}

	// Audit log
	sessionIDPtr := sessionID
	h.hub.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &sessionIDPtr,
		Action:    models.ActionSessionFromTemplate,
		Metadata: map[string]interface{}{
			"session_id":    session.ID.String(),
			"template_id":   templateID.String(),
			"template_slug": tmpl.Slug,
		},
	})

	c.JSON(http.StatusCreated, gin.H{
		"session":  session,
		"template": tmpl,
	})
}
