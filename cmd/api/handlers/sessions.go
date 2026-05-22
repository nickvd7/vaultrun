package handlers

import (
	"context"
	"database/sql"
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
	// AllowedHosts is an optional list of hostnames or CIDRs the container
	// may reach when network_enabled is true. Entries are resolved to
	// /etc/hosts entries at container creation time for DNS-level control.
	AllowedHosts   []string           `json:"allowed_hosts"`
	// OrgID optionally assigns this session to an org for shared access.
	// The caller must be an executor or admin of that org.
	OrgID          *string            `json:"org_id"`
	// SnapshotID optionally names a snapshot to restore into the new session workspace.
	SnapshotID *string `json:"snapshot_id"`
	// GPUEnabled requests GPU device passthrough when the server has GPUs configured.
	GPUEnabled bool    `json:"gpu_enabled"`
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

	// Resolve and validate org_id if provided.
	var orgID *uuid.UUID
	if req.OrgID != nil && *req.OrgID != "" {
		oid, err := uuid.Parse(*req.OrgID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
			return
		}
		// Verify the org exists.
		if _, err := dbpkg.GetOrg(c.Request.Context(), sh.h.db, oid); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "org not found"})
			return
		}
		// Non-master callers must be at least executor in that org.
		if actor != "master" {
			role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), sh.h.db, oid, actor)
			if err != nil || !models.RoleAtLeast(role, models.OrgRoleExecutor) {
				c.JSON(http.StatusForbidden, gin.H{"error": "org executor role required to create org sessions"})
				return
			}
		}
		orgID = &oid
	}

	sessionID := uuid.New()

	wspath, err := sh.h.ws.Create(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "workspace init failed"})
		return
	}

	if req.SnapshotID != nil && *req.SnapshotID != "" {
		snapUUID, err := uuid.Parse(*req.SnapshotID)
		if err != nil {
			_ = sh.h.ws.Delete(sessionID)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid snapshot_id"})
			return
		}
		snap, err := dbpkg.GetSnapshot(c.Request.Context(), sh.h.db, snapUUID)
		if err == sql.ErrNoRows {
			_ = sh.h.ws.Delete(sessionID)
			c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
			return
		}
		if err != nil {
			_ = sh.h.ws.Delete(sessionID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load snapshot"})
			return
		}
		if err := sh.h.ws.RestoreSnapshot(sessionID, snap.ArchivePath); err != nil {
			_ = sh.h.ws.Delete(sessionID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "restore snapshot failed: " + err.Error()})
			return
		}
	}

	now := time.Now().UTC()

	// Convert caller-supplied labels (string map) to JSONB.
	labels := models.JSONB{}
	for k, v := range req.Labels {
		labels[k] = v
	}

	allowedHosts := models.StringArray(req.AllowedHosts)
	if allowedHosts == nil {
		allowedHosts = models.StringArray{}
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
		AllowedHosts:   allowedHosts,
		CreatedBy:      actor,
		OrgID:          orgID,
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

	gpuDevices := ""
	if req.GPUEnabled && sh.h.cfg.Docker.GPUDevices != "" {
		gpuDevices = sh.h.cfg.Docker.GPUDevices
	}

	var containerID string
	if sh.h.warmPool != nil && req.Image == sh.h.cfg.Docker.WarmPoolImage {
		if entry, ok := sh.h.warmPool.Acquire(); ok {
			containerID = entry.ContainerID
			slog.Info("warm pool container acquired", "session_id", sessionID, "container_id", containerID)
		}
	}

	if containerID == "" {
		containerID, err = sh.h.docker.CreateSandbox(c.Request.Context(), dockerpkg.SandboxConfig{
			SessionID:      sessionID,
			Image:          req.Image,
			WorkspacePath:  wspath,
			NetworkEnabled: req.NetworkEnabled,
			CPULimit:       req.CPULimit,
			MemoryLimitMB:  req.MemoryLimitMB,
			ContainerName:  containerName,
			AllowedHosts:   []string(allowedHosts),
			GPUDevices:     gpuDevices,
		})
	}
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
// Returns the caller's own sessions plus sessions in any org they belong to.
// Master key sees all sessions. Supports optional ?label=key:value filter.
func (sh *SessionHandler) List(c *gin.Context) {
	pg := pagination(c)
	// Non-master callers see their own sessions + sessions shared via org membership.
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

	sessions, err := dbpkg.ListSessionsForActor(c.Request.Context(), sh.h.db, listActor, labelKey, labelValue, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}
	total, _ := dbpkg.CountSessionsForActor(c.Request.Context(), sh.h.db, listActor, labelKey, labelValue)
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "pagination": pg.response(total)})
}

// GET /api/v1/sessions/:id
func (sh *SessionHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	session, ok := sh.h.checkSessionAccess(c, id, models.OrgRoleViewer)
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
	session, ok := sh.h.checkSessionAccess(c, id, models.OrgRoleAdmin)
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

	if _, ok := sh.h.checkSessionAccess(c, id, models.OrgRoleExecutor); !ok {
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
