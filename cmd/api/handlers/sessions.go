package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/metrics"
	"github.com/nickvd7/vaultrun/internal/models"
)

type SessionHandler struct {
	h *Hub
}

func NewSessionHandler(h *Hub) *SessionHandler { return &SessionHandler{h: h} }

type createSessionRequest struct {
	Name           *string            `json:"name"`
	Image          string             `json:"image"`
	NetworkEnabled bool               `json:"network_enabled"`
	CPULimit       float64            `json:"cpu_limit"`
	MemoryLimitMB  int                `json:"memory_limit_mb"`
	TimeoutSeconds int                `json:"timeout_seconds"`
	Labels         map[string]string  `json:"labels"`
}

// POST /api/v1/sessions
func (sh *SessionHandler) Create(c *gin.Context) {
	var req createSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	limits := sh.h.cfg.SessionLimits()

	// Apply defaults for zero values
	if req.Image == "" {
		req.Image = sh.h.cfg.Docker.DefaultImage
	}
	if req.CPULimit <= 0 {
		req.CPULimit = 1.0
	}
	if req.MemoryLimitMB <= 0 {
		req.MemoryLimitMB = 512
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 300
	}

	// Enforce hard upper bounds to prevent resource exhaustion
	if req.CPULimit > limits.MaxCPU {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("cpu_limit exceeds maximum of %.1f", limits.MaxCPU)})
		return
	}
	if req.MemoryLimitMB > limits.MaxMemoryMB {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("memory_limit_mb exceeds maximum of %d", limits.MaxMemoryMB)})
		return
	}
	if req.TimeoutSeconds > limits.MaxTimeoutSec {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("timeout_seconds exceeds maximum of %d", limits.MaxTimeoutSec)})
		return
	}

	// Enforce image allowlist (when configured)
	if !sh.h.cfg.ImageAllowed(req.Image) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image not permitted"})
		return
	}

	// Enforce per-actor session quota (M-6). Master key is exempt.
	actor := middleware.Actor(c)
	if limits.MaxSessionsPerActor > 0 && actor != "master" {
		count, err := dbpkg.CountActiveSessionsByActor(c.Request.Context(), sh.h.db, actor)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count sessions"})
			return
		}
		if count >= limits.MaxSessionsPerActor {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": fmt.Sprintf("active session limit of %d reached", limits.MaxSessionsPerActor),
			})
			return
		}
	}

	sessionID := uuid.New()

	wspath, err := sh.h.ws.Create(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "workspace init failed"})
		return
	}

	now := time.Now().UTC()

	// Convert caller-supplied labels (string map) to JSONB.
	labels := models.JSONB{}
	for k, v := range req.Labels {
		labels[k] = v
	}

	session := &models.Session{
		ID:             sessionID,
		Name:           req.Name,
		Image:          req.Image,
		Status:         models.SessionStatusCreated,
		NetworkEnabled: req.NetworkEnabled,
		CPULimit:       req.CPULimit,
		MemoryLimitMB:  req.MemoryLimitMB,
		TimeoutSeconds: req.TimeoutSeconds,
		WorkspacePath:  wspath,
		Labels:         labels,
		CreatedBy:      actor,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := dbpkg.CreateSession(c.Request.Context(), sh.h.db, session); err != nil {
		_ = sh.h.ws.Delete(sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "persist session failed"})
		return
	}

	// Create Docker container
	// Use the full UUID as the container name suffix to avoid birthday collisions
	// (8-char hex has ~1% collision at 65 K sessions).
	containerName := fmt.Sprintf("%s-%s", sh.h.cfg.Docker.ContainerPrefix, sessionID.String())
	containerID, err := sh.h.docker.CreateSandbox(c.Request.Context(), dockerpkg.SandboxConfig{
		SessionID:      sessionID,
		Image:          req.Image,
		WorkspacePath:  wspath,
		NetworkEnabled: req.NetworkEnabled,
		CPULimit:       req.CPULimit,
		MemoryLimitMB:  req.MemoryLimitMB,
		ContainerName:  containerName,
	})
	if err != nil {
		slog.Error("container creation failed",
			"session_id", sessionID,
			"image", req.Image,
			"err", err,
		)
		_ = dbpkg.UpdateSessionStatus(c.Request.Context(), sh.h.db, sessionID, models.SessionStatusError, nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "container creation failed"})
		return
	}

	if err := dbpkg.UpdateSessionStatus(c.Request.Context(), sh.h.db, sessionID, models.SessionStatusRunning, &containerID); err != nil {
		// Container is running but we failed to persist its ID — stop it now to
		// prevent an orphaned container that can never be cleaned up (H-3).
		_ = sh.h.docker.StopSandbox(context.Background(), containerID)
		_ = sh.h.ws.Delete(sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update session status failed"})
		return
	}
	session.ContainerID = &containerID
	session.Status = models.SessionStatusRunning

	sh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &sessionID,
		Action:    models.ActionSessionCreated,
		Metadata: models.JSONB{
			"image":           req.Image,
			"network_enabled": req.NetworkEnabled,
			"cpu_limit":       req.CPULimit,
			"memory_limit_mb": req.MemoryLimitMB,
			"timeout_seconds": req.TimeoutSeconds,
		},
	})

	metrics.ActiveSessions.Inc()
	c.JSON(http.StatusCreated, session)
}

// GET /api/v1/sessions
// Supports optional ?label=key:value filter.
func (sh *SessionHandler) List(c *gin.Context) {
	pg := pagination(c)
	// Non-master callers only see their own sessions (C-2 tenant isolation).
	listActor := middleware.Actor(c)
	if listActor == "master" {
		listActor = ""
	}

	// Optional label filter: ?label=key:value
	var labelKey, labelValue string
	if lv := c.Query("label"); lv != "" {
		if idx := strings.IndexByte(lv, ':'); idx > 0 {
			labelKey = lv[:idx]
			labelValue = lv[idx+1:]
		}
	}

	sessions, err := dbpkg.ListSessions(c.Request.Context(), sh.h.db, listActor, labelKey, labelValue, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}
	total, _ := dbpkg.CountSessionsFiltered(c.Request.Context(), sh.h.db, listActor, labelKey, labelValue)
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "pagination": pg.response(total)})
}

// GET /api/v1/sessions/:id
func (sh *SessionHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	session, ok := sh.h.checkSessionAccess(c, id)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, session)
}

// DELETE /api/v1/sessions/:id
func (sh *SessionHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	session, ok := sh.h.checkSessionAccess(c, id)
	if !ok {
		return
	}

	// Stop container — log failure but continue to clean up DB and workspace.
	if session.ContainerID != nil {
		if err := sh.h.docker.StopSandbox(c.Request.Context(), *session.ContainerID); err != nil {
			slog.Warn("container stop failed during session delete",
				"session_id", id,
				"container_id", *session.ContainerID,
				"err", err,
			)
		}
	}

	// Remove workspace
	if err := sh.h.ws.Delete(id); err != nil {
		slog.Warn("workspace delete failed", "session_id", id, "err", err)
	}

	if err := dbpkg.StopSession(c.Request.Context(), sh.h.db, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop session"})
		return
	}

	actor := middleware.Actor(c)
	sh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &id,
		Action:    models.ActionSessionDeleted,
		Metadata:  models.JSONB{},
	})

	metrics.ActiveSessions.Dec()
	c.JSON(http.StatusOK, gin.H{"message": "session deleted"})
}

// PATCH /api/v1/sessions/:id/labels
// Replaces the session's entire label set. Pass {} to clear all labels.
func (sh *SessionHandler) UpdateLabels(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if _, ok := sh.h.checkSessionAccess(c, id); !ok {
		return
	}

	var body struct {
		Labels map[string]string `json:"labels" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	labels := models.JSONB{}
	for k, v := range body.Labels {
		labels[k] = v
	}

	if err := dbpkg.UpdateSessionLabels(c.Request.Context(), sh.h.db, id, labels); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update labels"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"labels": labels})
}
