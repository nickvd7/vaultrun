package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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

	session, ok := rh.h.checkSessionAccess(c, sessionID)
	if !ok {
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

	limits := rh.h.cfg.SessionLimits()
	if req.TimeoutSeconds < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "timeout_seconds must be non-negative"})
		return
	}
	if req.TimeoutSeconds > limits.MaxTimeoutSec {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("timeout_seconds exceeds maximum of %d", limits.MaxTimeoutSec)})
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

// POST /api/v1/sessions/:id/run/stream
// Same request body as Execute, but responds with text/event-stream (SSE).
// Events:
//
//	data: {"type":"stdout","data":"..."}\n\n
//	data: {"type":"stderr","data":"..."}\n\n
//	data: {"type":"done","run_id":"...","exit_code":0,"status":"completed","duration_ms":123}\n\n
func (rh *RunHandler) Stream(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	session, ok := rh.h.checkSessionAccess(c, sessionID)
	if !ok {
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

	limits := rh.h.cfg.SessionLimits()
	if req.TimeoutSeconds < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "timeout_seconds must be non-negative"})
		return
	}
	if req.TimeoutSeconds > limits.MaxTimeoutSec {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("timeout_seconds exceeds maximum of %d", limits.MaxTimeoutSec)})
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	stdoutW := &sseWriter{w: c.Writer, flusher: flusher, kind: "stdout"}
	stderrW := &sseWriter{w: c.Writer, flusher: flusher, kind: "stderr"}

	run, runErr := rh.h.runner.Stream(c.Request.Context(), runner.RunRequest{
		SessionID:      sessionID,
		ContainerID:    *session.ContainerID,
		Command:        req.Command,
		Args:           req.Args,
		Env:            req.Env,
		WorkingDir:     req.WorkingDir,
		TimeoutSeconds: req.TimeoutSeconds,
		Actor:          middleware.Actor(c),
	}, stdoutW, stderrW)

	// Send final done event regardless of error
	doneEvent := map[string]any{"type": "done"}
	if runErr != nil {
		doneEvent["error"] = runErr.Error()
	} else if run != nil {
		doneEvent["run_id"] = run.ID
		doneEvent["status"] = run.Status
		doneEvent["duration_ms"] = run.DurationMS
		if run.ExitCode != nil {
			doneEvent["exit_code"] = *run.ExitCode
		}
		if run.OutputTruncated {
			doneEvent["output_truncated"] = true
		}
	}
	writeSSEEvent(c.Writer, flusher, doneEvent)
}

// GET /api/v1/sessions/:id/runs
func (rh *RunHandler) ListBySession(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := rh.h.checkSessionAccess(c, sessionID); !ok {
		return
	}

	pg := pagination(c)
	runs, err := dbpkg.ListRuns(c.Request.Context(), rh.h.db, sessionID, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list runs"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get run"})
		return
	}

	// Ownership check: verify the caller owns the parent session (H-5).
	// This prevents cross-tenant run data leakage via direct UUID access.
	actor := middleware.Actor(c)
	if actor != "master" {
		session, sessErr := dbpkg.GetSession(c.Request.Context(), rh.h.db, run.SessionID)
		if sessErr == sql.ErrNoRows || (sessErr == nil && session.CreatedBy != actor) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		if sessErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify run ownership"})
			return
		}
	}

	c.JSON(http.StatusOK, run)
}

// sseWriter wraps an http.ResponseWriter and formats each Write call as an SSE data event.
type sseWriter struct {
	w       io.Writer
	flusher http.Flusher
	kind    string // "stdout" or "stderr"
}

func (s *sseWriter) Write(p []byte) (int, error) {
	event := map[string]any{"type": s.kind, "data": string(p)}
	writeSSEEvent(s.w, s.flusher, event)
	return len(p), nil
}

func writeSSEEvent(w io.Writer, flusher http.Flusher, event map[string]any) {
	b, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}
