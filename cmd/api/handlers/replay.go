package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/replay"
)

type ReplayHandler struct {
	h   *Hub
	mgr *replay.Manager
}

func NewReplayHandler(h *Hub, mgr *replay.Manager) *ReplayHandler {
	return &ReplayHandler{h: h, mgr: mgr}
}

type createCheckpointRequest struct {
	RunID       *string           `json:"run_id,omitempty"`
	Name        *string           `json:"name,omitempty"`
	Description string            `json:"description"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	ExitCode    *int              `json:"exit_code,omitempty"`
	DurationMs  *int              `json:"duration_ms,omitempty"`
	Stdout      string            `json:"stdout,omitempty"`
	Stderr      string            `json:"stderr,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
}

type restoreCheckpointRequest struct {
	CheckpointID       string `json:"checkpoint_id"`
	StopActiveCommands bool   `json:"stop_active_commands"`
}

type forkCheckpointRequest struct {
	CheckpointID  string   `json:"checkpoint_id"`
	Name          string   `json:"name"`
	CPULimit      *float64 `json:"cpu_limit,omitempty"`
	MemoryLimitMB *int     `json:"memory_limit_mb,omitempty"`
}

// POST /api/v1/sessions/:id/checkpoints
func (rh *ReplayHandler) CreateCheckpoint(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	var req createCheckpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify session exists and caller has access
	session, err := rh.h.sessionManager.GetSession(c.Request.Context(), sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		slog.Error("get session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session"})
		return
	}

	// Check authorization
	actor := middleware.Actor(c)
	if actor != "master" && session.CreatedBy != actor {
		// Check org membership if session has an org
		if session.OrgID != nil {
			orgAccess, _ := rh.h.orgManager.GetUserOrgRole(c.Request.Context(), actor, *session.OrgID)
			if orgAccess == nil {
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
				return
			}
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	// Parse optional run ID
	var runID *uuid.UUID
	if req.RunID != nil && *req.RunID != "" {
		parsed, err := uuid.Parse(*req.RunID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run_id"})
			return
		}
		runID = &parsed
	}

	// Create checkpoint
	checkpoint, err := rh.mgr.CreateCheckpoint(c.Request.Context(), replay.CreateCheckpointOpts{
		SessionID:   sessionID,
		RunID:       runID,
		Name:        req.Name,
		Description: req.Description,
		Command:     req.Command,
		Args:        req.Args,
		ExitCode:    req.ExitCode,
		DurationMs:  req.DurationMs,
		Stdout:      req.Stdout,
		Stderr:      req.Stderr,
		EnvVars:     req.EnvVars,
	})

	if err == replay.ErrCheckpointLimitExceeded {
		c.JSON(http.StatusConflict, gin.H{"error": "checkpoint limit exceeded"})
		return
	}
	if err == replay.ErrCheckpointTooLarge {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "checkpoint too large"})
		return
	}
	if err == replay.ErrReplayDisabled {
		c.JSON(http.StatusConflict, gin.H{
			"error": "replay is not enabled for this session; set replay_enabled when creating it",
		})
		return
	}
	if err == replay.ErrSessionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if errors.Is(err, replay.ErrInvalidCheckpoint) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err == replay.ErrOrgStorageLimitExceeded {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "organization storage limit exceeded"})
		return
	}
	if err != nil {
		slog.Error("create checkpoint", "err", err, "session_id", sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create checkpoint"})
		return
	}

	// Audit log
	rh.h.audit.Log(c.Request.Context(), audit.Event{
		Action:    models.ActionCheckpointCreated,
		Actor:     actor,
		SessionID: &sessionID,
		Metadata: map[string]interface{}{
			"checkpoint_id":     checkpoint.ID.String(),
			"checkpoint_number": checkpoint.CheckpointNumber,
			"name":              checkpoint.Name,
		},
	})

	c.JSON(http.StatusCreated, checkpoint)
}

// GET /api/v1/sessions/:id/checkpoints
func (rh *ReplayHandler) ListCheckpoints(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	// Verify session exists and caller has access
	session, err := rh.h.sessionManager.GetSession(c.Request.Context(), sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		slog.Error("get session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session"})
		return
	}

	// Check authorization
	actor := middleware.Actor(c)
	if actor != "master" && session.CreatedBy != actor {
		if session.OrgID != nil {
			orgAccess, _ := rh.h.orgManager.GetUserOrgRole(c.Request.Context(), actor, *session.OrgID)
			if orgAccess == nil {
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
				return
			}
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	// Parse pagination. Values outside the accepted range are clamped rather
	// than rejected, matching the rest of the API; the manager clamps again so
	// no caller can smuggle a negative LIMIT through to Postgres.
	limit := replay.DefaultCheckpointLimit
	offset := 0
	if v, err := strconv.Atoi(c.Query("limit")); err == nil {
		limit = v
	}
	if v, err := strconv.Atoi(c.Query("offset")); err == nil {
		offset = v
	}
	if limit <= 0 {
		limit = replay.DefaultCheckpointLimit
	}
	if limit > replay.MaxCheckpointListLimit {
		limit = replay.MaxCheckpointListLimit
	}
	if offset < 0 {
		offset = 0
	}

	checkpoints, err := rh.mgr.ListCheckpoints(c.Request.Context(), replay.ListOpts{
		SessionID: sessionID,
		Limit:     limit,
		Offset:    offset,
	})

	if err != nil {
		slog.Error("list checkpoints", "err", err, "session_id", sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list checkpoints"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"checkpoints": checkpoints,
		"limit":       limit,
		"offset":      offset,
	})
}

// GET /api/v1/sessions/:id/checkpoints/:checkpoint_id
func (rh *ReplayHandler) GetCheckpoint(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	checkpointID, err := uuid.Parse(c.Param("checkpoint_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid checkpoint id"})
		return
	}

	// Get checkpoint
	checkpoint, err := rh.mgr.GetCheckpoint(c.Request.Context(), checkpointID)
	if err == replay.ErrCheckpointNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}
	if err != nil {
		slog.Error("get checkpoint", "err", err, "checkpoint_id", checkpointID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get checkpoint"})
		return
	}

	// Verify it belongs to this session
	if checkpoint.SessionID != sessionID {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}

	// Verify session access
	session, err := rh.h.sessionManager.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify access"})
		return
	}

	actor := middleware.Actor(c)
	if actor != "master" && session.CreatedBy != actor {
		if session.OrgID != nil {
			orgAccess, _ := rh.h.orgManager.GetUserOrgRole(c.Request.Context(), actor, *session.OrgID)
			if orgAccess == nil {
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
				return
			}
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	c.JSON(http.StatusOK, checkpoint)
}

// POST /api/v1/sessions/:id/checkpoints/:checkpoint_id/restore
func (rh *ReplayHandler) RestoreCheckpoint(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	checkpointID, err := uuid.Parse(c.Param("checkpoint_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid checkpoint id"})
		return
	}

	var req restoreCheckpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body
		req.StopActiveCommands = false
	}

	// Verify session access
	session, err := rh.h.sessionManager.GetSession(c.Request.Context(), sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session"})
		return
	}

	actor := middleware.Actor(c)
	if actor != "master" && session.CreatedBy != actor {
		if session.OrgID != nil {
			orgAccess, _ := rh.h.orgManager.GetUserOrgRole(c.Request.Context(), actor, *session.OrgID)
			if orgAccess == nil || (orgAccess.Role != "admin" && orgAccess.Role != "executor") {
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied - requires admin or executor role"})
				return
			}
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	// Restore checkpoint
	err = rh.mgr.RestoreCheckpoint(c.Request.Context(), replay.RestoreOpts{
		SessionID:          sessionID,
		CheckpointID:       checkpointID,
		StopActiveCommands: req.StopActiveCommands,
	})

	if err == replay.ErrCheckpointNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}
	if err == replay.ErrCheckpointTampered {
		c.JSON(http.StatusConflict, gin.H{"error": "checkpoint signature invalid"})
		return
	}
	if err == replay.ErrActiveCommandsRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "active commands running - set stop_active_commands=true to force"})
		return
	}
	if err != nil {
		slog.Error("restore checkpoint", "err", err, "checkpoint_id", checkpointID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore checkpoint"})
		return
	}

	// Audit log
	rh.h.audit.Log(c.Request.Context(), audit.Event{
		Action:    models.ActionCheckpointRestored,
		Actor:     actor,
		SessionID: &sessionID,
		Metadata: map[string]interface{}{
			"checkpoint_id":        checkpointID.String(),
			"stop_active_commands": req.StopActiveCommands,
		},
	})

	c.JSON(http.StatusOK, gin.H{"status": "restored"})
}

// enforceForkLimits bounds the resource overrides on a fork request and applies
// the per-actor session quota. It writes the error response itself.
func (rh *ReplayHandler) enforceForkLimits(c *gin.Context, actor string, src *models.Session, req forkCheckpointRequest) error {
	limits := rh.h.cfg.SessionLimits()

	// A fork reruns the source session's image. That image was allowed when the
	// session was created, but the allowlist is the deployment's current answer
	// to "what may run here" — an image withdrawn since then (a known-vulnerable
	// base, say) must not come back through a fork.
	if !rh.h.cfg.ImageAllowed(src.Image) {
		err := errors.New("source session image is no longer permitted")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}

	if req.CPULimit != nil {
		if *req.CPULimit <= 0 {
			err := errors.New("cpu_limit must be greater than 0")
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return err
		}
		if *req.CPULimit > limits.MaxCPU {
			err := fmt.Errorf("cpu_limit exceeds maximum of %.1f", limits.MaxCPU)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return err
		}
	}

	if req.MemoryLimitMB != nil {
		if *req.MemoryLimitMB <= 0 {
			err := errors.New("memory_limit_mb must be greater than 0")
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return err
		}
		if *req.MemoryLimitMB > limits.MaxMemoryMB {
			err := fmt.Errorf("memory_limit_mb exceeds maximum of %d", limits.MaxMemoryMB)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return err
		}
	}

	if limits.MaxSessionsPerActor > 0 && actor != "master" {
		count, err := dbpkg.CountActiveSessionsByActor(c.Request.Context(), rh.h.db, actor)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count sessions"})
			return err
		}
		if count >= limits.MaxSessionsPerActor {
			qerr := fmt.Errorf("active session limit of %d reached", limits.MaxSessionsPerActor)
			c.JSON(http.StatusTooManyRequests, gin.H{"error": qerr.Error()})
			return qerr
		}
	}

	return nil
}

// POST /api/v1/checkpoints/:checkpoint_id/fork
func (rh *ReplayHandler) ForkCheckpoint(c *gin.Context) {
	checkpointID, err := uuid.Parse(c.Param("checkpoint_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid checkpoint id"})
		return
	}

	var req forkCheckpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get checkpoint to verify access
	checkpoint, err := rh.mgr.GetCheckpoint(c.Request.Context(), checkpointID)
	if err == replay.ErrCheckpointNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get checkpoint"})
		return
	}

	// Verify session access
	session, err := rh.h.sessionManager.GetSession(c.Request.Context(), checkpoint.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify access"})
		return
	}

	// A fork creates a running session, which is an executor-level action —
	// unlike reading a checkpoint, which a viewer may do.
	actor := middleware.Actor(c)
	if actor != "master" && session.CreatedBy != actor {
		if session.OrgID != nil {
			orgAccess, _ := rh.h.orgManager.GetUserOrgRole(c.Request.Context(), actor, *session.OrgID)
			if orgAccess == nil || !models.RoleAtLeast(orgAccess.Role, models.OrgRoleExecutor) {
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied - requires admin or executor role"})
				return
			}
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	// The fork inherits the source session's limits unless the caller overrides
	// them, and an override is a new session request: it passes the same bounds
	// and quota as POST /sessions.
	if err := rh.enforceForkLimits(c, actor, session, req); err != nil {
		return
	}

	// Fork checkpoint
	newSessionID, err := rh.mgr.ForkFromCheckpoint(c.Request.Context(), replay.ForkOpts{
		CheckpointID:  checkpointID,
		Name:          req.Name,
		Actor:         actor,
		CPULimit:      req.CPULimit,
		MemoryLimitMB: req.MemoryLimitMB,
	})

	if err == replay.ErrCheckpointNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}
	if err == replay.ErrSessionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err == replay.ErrCheckpointTampered {
		c.JSON(http.StatusConflict, gin.H{"error": "checkpoint signature invalid"})
		return
	}
	if errors.Is(err, replay.ErrInvalidCheckpoint) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		slog.Error("fork checkpoint", "err", err, "checkpoint_id", checkpointID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fork checkpoint"})
		return
	}

	// Audit log
	rh.h.audit.Log(c.Request.Context(), audit.Event{
		Action: models.ActionCheckpointForked,
		Actor:  actor,
		Metadata: map[string]interface{}{
			"checkpoint_id":  checkpointID.String(),
			"new_session_id": newSessionID.String(),
			"name":           req.Name,
		},
	})

	c.JSON(http.StatusCreated, gin.H{
		"session_id": newSessionID.String(),
	})
}

// DELETE /api/v1/sessions/:id/checkpoints/:checkpoint_id
func (rh *ReplayHandler) DeleteCheckpoint(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	checkpointID, err := uuid.Parse(c.Param("checkpoint_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid checkpoint id"})
		return
	}

	// Get checkpoint
	checkpoint, err := rh.mgr.GetCheckpoint(c.Request.Context(), checkpointID)
	if err == replay.ErrCheckpointNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get checkpoint"})
		return
	}

	// Verify it belongs to this session
	if checkpoint.SessionID != sessionID {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}

	// Verify session access
	session, err := rh.h.sessionManager.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify access"})
		return
	}

	actor := middleware.Actor(c)
	if actor != "master" && session.CreatedBy != actor {
		if session.OrgID != nil {
			orgAccess, _ := rh.h.orgManager.GetUserOrgRole(c.Request.Context(), actor, *session.OrgID)
			if orgAccess == nil || (orgAccess.Role != "admin" && orgAccess.Role != "executor") {
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied - requires admin or executor role"})
				return
			}
		} else {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	// Delete checkpoint
	err = rh.mgr.DeleteCheckpoint(c.Request.Context(), checkpointID)
	if err != nil {
		slog.Error("delete checkpoint", "err", err, "checkpoint_id", checkpointID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete checkpoint"})
		return
	}

	// Audit log
	rh.h.audit.Log(c.Request.Context(), audit.Event{
		Action:    models.ActionCheckpointDeleted,
		Actor:     actor,
		SessionID: &sessionID,
		Metadata: map[string]interface{}{
			"checkpoint_id": checkpointID.String(),
		},
	})

	c.JSON(http.StatusNoContent, nil)
}
