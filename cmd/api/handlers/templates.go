package handlers

import (
	"errors"
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	// Get caller's org (use first org the user belongs to)
	actor := middleware.Actor(c)
	var orgID uuid.UUID
	err := h.hub.db.GetContext(c.Request.Context(), &orgID,
		"SELECT org_id FROM org_members WHERE principal = $1 LIMIT 1", actor)
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	// TODO: Check if user owns this template or is admin

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	// TODO: Check if user owns this template or is admin

	err = h.manager.Delete(c.Request.Context(), id)
	if err == templates.ErrTemplateNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get user's org (use first org the user belongs to)
	var orgID uuid.UUID
	err = h.hub.db.GetContext(c.Request.Context(), &orgID,
		"SELECT org_id FROM org_members WHERE principal = $1 LIMIT 1", actor)
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
		OrgID:          &orgID,
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

	// Record usage
	err = h.manager.RecordUsage(c.Request.Context(), templateID, sessionID, orgID)
	if err != nil {
		// Non-fatal, just log
		// TODO: Add proper logging
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
