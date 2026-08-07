package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/verify"
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Spec.Empty() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "spec must include at least one check"})
		return
	}

	obs := verify.Observation{}
	if req.Observation != nil {
		obs = *req.Observation
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
			obs.Stdout = *run.Stdout
		}
		if obs.Stderr == "" && run.Stderr != nil {
			obs.Stderr = *run.Stderr
		}
	} else if sessionID != nil {
		if _, ok := vh.h.checkSessionAccess(c, *sessionID, models.OrgRoleViewer); !ok {
			return
		}
	} else if req.Spec.FileExists != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id or run_id required for file_exists"})
		return
	}

	var probe verify.FileProbe
	if sessionID != nil && req.Spec.FileExists != "" {
		sid := *sessionID
		probe = func(path string) (bool, error) {
			full, err := vh.h.ws.SafePath(sid, path)
			if err != nil {
				return false, err
			}
			_, err = os.Stat(full)
			if os.IsNotExist(err) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return true, nil
		}
	}

	result := verify.Evaluate(req.Spec, obs, probe)

	persist := true
	if req.Persist != nil {
		persist = *req.Persist
	}

	var recordID *uuid.UUID
	if persist && vh.store != nil {
		specJSON, _ := json.Marshal(req.Spec)
		obsJSON, _ := json.Marshal(obs)
		checksJSON, _ := json.Marshal(result.Checks)
		rec := &verify.Record{
			SessionID:    sessionID,
			RunID:        req.RunID,
			MissionRunID: req.MissionRunID,
			StepName:     req.StepName,
			Spec:         specJSON,
			Observation:  obsJSON,
			Passed:       result.Passed,
			Checks:       checksJSON,
		}
		if err := vh.store.Save(c.Request.Context(), rec); err != nil {
			slog.Error("persist verification", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist verification"})
			return
		}
		recordID = &rec.ID
	}

	c.JSON(http.StatusOK, gin.H{
		"passed":    result.Passed,
		"checks":    result.Checks,
		"id":        recordID,
		"session_id": sessionID,
		"run_id":    req.RunID,
	})
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
