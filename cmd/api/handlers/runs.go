package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/runner"
)

type RunHandler struct {
	h *Hub
}

func NewRunHandler(h *Hub) *RunHandler { return &RunHandler{h: h} }

type createRunRequest struct {
	Command        string            `json:"command" binding:"required"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env"`
	WorkingDir     string            `json:"working_dir"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// POST /api/v1/sessions/:id/run
func (rh *RunHandler) Execute(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	session, err := dbpkg.GetSession(c.Request.Context(), rh.h.db, sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if session.StoppedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "session is stopped"})
		return
	}
	if session.ContainerID == nil || session.Status != models.SessionStatusRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "session container is not running"})
		return
	}

	var req createRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	run, err := rh.h.runner.Execute(c.Request.Context(), runner.RunRequest{
		SessionID:      sessionID,
		ContainerID:    *session.ContainerID,
		Command:        req.Command,
		Args:           req.Args,
		Env:            req.Env,
		WorkingDir:     req.WorkingDir,
		TimeoutSeconds: req.TimeoutSeconds,
		Actor:          middleware.Actor(c),
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, run)
}

// GET /api/v1/sessions/:id/runs
func (rh *RunHandler) ListBySession(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if _, err := dbpkg.GetSession(c.Request.Context(), rh.h.db, sessionID); err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	pg := pagination(c)
	runs, err := dbpkg.ListRuns(c.Request.Context(), rh.h.db, sessionID, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"runs": runs, "pagination": pg})
}

// GET /api/v1/runs/:id
func (rh *RunHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	run, err := dbpkg.GetRun(c.Request.Context(), rh.h.db, id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, run)
}
