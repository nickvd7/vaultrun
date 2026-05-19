package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/models"
)

type SessionHandler struct {
	h *Hub
}

func NewSessionHandler(h *Hub) *SessionHandler { return &SessionHandler{h: h} }

type createSessionRequest struct {
	Name           *string `json:"name"`
	Image          string  `json:"image"`
	NetworkEnabled bool    `json:"network_enabled"`
	CPULimit       float64 `json:"cpu_limit"`
	MemoryLimitMB  int     `json:"memory_limit_mb"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}

// POST /api/v1/sessions
func (sh *SessionHandler) Create(c *gin.Context) {
	var req createSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Defaults
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

	sessionID := uuid.New()

	wspath, err := sh.h.ws.Create(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "workspace init failed"})
		return
	}

	actor := middleware.Actor(c)
	now := time.Now().UTC()

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
	containerName := fmt.Sprintf("%s-%s", sh.h.cfg.Docker.ContainerPrefix, sessionID.String()[:8])
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
		// Session exists but container failed — mark as error
		_ = dbpkg.UpdateSessionStatus(c.Request.Context(), sh.h.db, sessionID, models.SessionStatusError, nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "container creation failed: " + err.Error()})
		return
	}

	if err := dbpkg.UpdateSessionStatus(c.Request.Context(), sh.h.db, sessionID, models.SessionStatusRunning, &containerID); err != nil {
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
			"image":            req.Image,
			"network_enabled":  req.NetworkEnabled,
			"cpu_limit":        req.CPULimit,
			"memory_limit_mb":  req.MemoryLimitMB,
			"timeout_seconds":  req.TimeoutSeconds,
		},
	})

	c.JSON(http.StatusCreated, session)
}

// GET /api/v1/sessions
func (sh *SessionHandler) List(c *gin.Context) {
	pg := pagination(c)
	sessions, err := dbpkg.ListSessions(c.Request.Context(), sh.h.db, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "pagination": pg})
}

// GET /api/v1/sessions/:id
func (sh *SessionHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	session, err := dbpkg.GetSession(c.Request.Context(), sh.h.db, id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	session, err := dbpkg.GetSession(c.Request.Context(), sh.h.db, id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Stop container
	if session.ContainerID != nil {
		if err := sh.h.docker.StopSandbox(c.Request.Context(), *session.ContainerID); err != nil {
			// Log but continue — we still want to clean up the DB record
			_ = err
		}
	}

	// Remove workspace
	_ = sh.h.ws.Delete(id)

	if err := dbpkg.StopSession(c.Request.Context(), sh.h.db, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	actor := middleware.Actor(c)
	sh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &id,
		Action:    models.ActionSessionDeleted,
		Metadata:  models.JSONB{},
	})

	c.JSON(http.StatusOK, gin.H{"message": "session deleted"})
}

