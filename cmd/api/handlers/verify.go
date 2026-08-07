package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/verify"
)

const (
	verifyMaxStdoutBytes = 256 * 1024
	verifyMaxStepName    = 200
)

// VerifyHandler evaluates and optionally persists post-run checkpoints.
type VerifyHandler struct {
	h     *Hub
	store *verify.Store
}

// NewVerifyHandler creates a VerifyHandler.
func NewVerifyHandler(h *Hub, store *verify.Store) *VerifyHandler {
	return &VerifyHandler{h: h, store: store}
}

type verifyRequest struct {
	Spec         verify.Spec         `json:"spec" binding:"required"`
	Observation  *verify.Observation `json:"observation"`
	RunID        *uuid.UUID          `json:"run_id"`
	SessionID    *uuid.UUID          `json:"session_id"`
	MissionRunID *uuid.UUID          `json:"mission_run_id"`
	StepName     string              `json:"step_name"`
	Persist      *bool               `json:"persist"`
}

// Evaluate POST /api/v1/verify
func (vh *VerifyHandler) Evaluate(c *gin.Context) {
	var req verifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Spec.Empty() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "spec must include at least one check"})
		return
	}
	if utf8.RuneCountInString(req.StepName) > verifyMaxStepName {
		c.JSON(http.StatusBadRequest, gin.H{"error": "step_name too long"})
		return
	}

	obs := verify.Observation{}
	if req.Observation != nil {
		obs = *req.Observation
	}
	if len(obs.Stdout) > verifyMaxStdoutBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "observation.stdout too large"})
		return
	}
	if len(obs.Stderr) > verifyMaxStdoutBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "observation.stderr too large"})
		return
	}

	var sessionID *uuid.UUID
	if req.SessionID != nil {
		sessionID = req.SessionID
	}

	if req.RunID != nil {
		run, err := dbpkg.GetRun(c.Request.Context(), vh.h.db, *req.RunID)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		if err != nil {
			slog.Error("verify get run", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load run"})
			return
		}
		if _, ok := vh.h.checkSessionAccess(c, run.SessionID, models.OrgRoleViewer); !ok {
			return
		}
		sid := run.SessionID
		sessionID = &sid
		if obs.ExitCode == nil {
			obs.ExitCode = run.ExitCode
		}
		if obs.Stdout == "" && run.Stdout != nil {
			obs.Stdout = truncateVerifyBytes(*run.Stdout, verifyMaxStdoutBytes)
		}
		if obs.Stderr == "" && run.Stderr != nil {
			obs.Stderr = truncateVerifyBytes(*run.Stderr, verifyMaxStdoutBytes)
		}
	} else if sessionID != nil {
		if _, ok := vh.h.checkSessionAccess(c, *sessionID, models.OrgRoleViewer); !ok {
			return
		}
	} else if req.Spec.FileExists != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id or run_id required for file_exists"})
		return
	}

	// Persist is a write — viewers may evaluate ephemerally, not append audit rows.
	persist := false
	if req.Persist != nil {
		persist = *req.Persist
	} else if sessionID != nil || req.RunID != nil {
		persist = true
	}
	if persist {
		if sessionID == nil && req.RunID == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "session_id or run_id required to persist"})
			return
		}
		sid := uuid.Nil
		if sessionID != nil {
			sid = *sessionID
		}
		if _, ok := vh.h.checkSessionAccess(c, sid, models.OrgRoleExecutor); !ok {
			return
		}
	}

	// Ignore unvalidated mission_run_id until missions FK exists on this branch.
	req.MissionRunID = nil

	var probe verify.FileProbe
	if sessionID != nil && req.Spec.FileExists != "" {
		sid := *sessionID
		probe = func(path string) (bool, error) {
			return vh.h.ws.Exists(sid, path)
		}
	}

	result := verify.Evaluate(req.Spec, obs, probe)

	var recordID *uuid.UUID
	if persist && vh.store != nil {
		specJSON, _ := json.Marshal(req.Spec)
		obsJSON, _ := json.Marshal(obs)
		checksJSON, _ := json.Marshal(result.Checks)
		rec := &verify.Record{
			SessionID:   sessionID,
			RunID:       req.RunID,
			StepName:    req.StepName,
			Spec:        specJSON,
			Observation: obsJSON,
			Passed:      result.Passed,
			Checks:      checksJSON,
		}
		if err := vh.store.Save(c.Request.Context(), rec); err != nil {
			slog.Error("persist verification", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist verification"})
			return
		}
		recordID = &rec.ID
	}

	c.JSON(http.StatusOK, gin.H{
		"passed":     result.Passed,
		"checks":     result.Checks,
		"id":         recordID,
		"session_id": sessionID,
		"run_id":     req.RunID,
	})
}

func truncateVerifyBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// ListBySession GET /api/v1/sessions/:id/verifications
func (vh *VerifyHandler) ListBySession(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := vh.h.checkSessionAccess(c, sessionID, models.OrgRoleViewer); !ok {
		return
	}
	if vh.store == nil {
		c.JSON(http.StatusOK, gin.H{"verifications": []any{}})
		return
	}
	list, err := vh.store.ListBySession(c.Request.Context(), sessionID, 50)
	if err != nil {
		slog.Error("list verifications", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list verifications"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"verifications": list})
}
