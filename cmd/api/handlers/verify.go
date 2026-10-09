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

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
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
	h       *Hub
	store   *verify.Store
	hmacKey []byte // AUDIT_HMAC_KEY; empty disables evidence signatures
}

// NewVerifyHandler creates a VerifyHandler.
func NewVerifyHandler(h *Hub, store *verify.Store) *VerifyHandler {
	vh := &VerifyHandler{h: h, store: store}
	if h != nil && h.cfg != nil && h.cfg.Observability.AuditHMACKey != "" {
		vh.hmacKey = []byte(h.cfg.Observability.AuditHMACKey)
	}
	return vh
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

// Controls POST /api/v1/verify/controls
// Runs the frozen positive/negative control suite against the in-process
// evaluator (no Docker / session required). Certifies that verify discriminates
// known-good from known-bad observations — FaultWright-style evaluator check.
func (vh *VerifyHandler) Controls(c *gin.Context) {
	if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodGet {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "GET or POST only"})
		return
	}
	report := verify.RunControls()
	status := http.StatusOK
	if !report.Passed {
		// Suite failure is a product/invariant break, not a client error.
		status = http.StatusInternalServerError
	}
	c.JSON(status, report)
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

type evidenceRequest struct {
	Spec             *verify.Spec         `json:"spec"`
	Observation      *verify.Observation  `json:"observation"`
	RunID            *uuid.UUID           `json:"run_id"`
	SessionID        *uuid.UUID           `json:"session_id"`
	VerificationID   *uuid.UUID           `json:"verification_id"`
	StepName         string               `json:"step_name"`
	IncludeControls  *bool                `json:"include_controls"`
	Sign             *bool                `json:"sign"` // default true when AUDIT_HMAC_KEY set
}

// Evidence POST /api/v1/verify/evidence
// Seals a digests-and-optional-HMAC verification export (checkpoint and/or controls).
func (vh *VerifyHandler) Evidence(c *gin.Context) {
	var req evidenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if utf8.RuneCountInString(req.StepName) > verifyMaxStepName {
		c.JSON(http.StatusBadRequest, gin.H{"error": "step_name too long"})
		return
	}

	includeControls := false
	if req.IncludeControls != nil {
		includeControls = *req.IncludeControls
	}

	in := verify.EvidenceInput{
		Actor:           middleware.Actor(c),
		StepName:        req.StepName,
		IncludeControls: includeControls,
	}

	// Path A: load a persisted verification by id (ACL via its session).
	if req.VerificationID != nil {
		if vh.store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "verification store unavailable"})
			return
		}
		rec, err := vh.store.Get(c.Request.Context(), *req.VerificationID)
		if errors.Is(err, verify.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "verification not found"})
			return
		}
		if err != nil {
			slog.Error("evidence get verification", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load verification"})
			return
		}
		if !vh.authorizeVerificationRead(c, rec) {
			return
		}
		if err := populateEvidenceFromRecord(&in, rec); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupt verification record"})
			return
		}
		vid := rec.ID
		in.VerificationID = &vid
		if req.StepName == "" {
			in.StepName = rec.StepName
		}
	} else if req.Spec != nil {
		// Path B: evaluate inline (same ACL rules as Evaluate).
		if req.Spec.Empty() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "spec must include at least one check"})
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

		sessionID := req.SessionID
		if req.RunID != nil {
			run, err := dbpkg.GetRun(c.Request.Context(), vh.h.db, *req.RunID)
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
				return
			}
			if err != nil {
				slog.Error("evidence get run", "err", err)
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
			in.RunID = req.RunID
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
				return vh.h.ws.Exists(sid, path)
			}
		}
		result := verify.Evaluate(*req.Spec, obs, probe)
		in.HasCheckpoint = true
		in.Spec = *req.Spec
		in.Observation = obs
		in.Result = result
		in.SessionID = sessionID
		in.RunID = req.RunID
	} else if !includeControls {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provide verification_id, spec, and/or include_controls"})
		return
	}

	sign := len(vh.hmacKey) > 0
	if req.Sign != nil {
		sign = *req.Sign && len(vh.hmacKey) > 0
	}
	if sign {
		in.HMACKey = vh.hmacKey
	}

	rec, err := verify.BuildEvidence(in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rec)
}

// GetEvidence GET /api/v1/verifications/:id/evidence
// Exports a sealed evidence record for a persisted verification (ACL via session).
func (vh *VerifyHandler) GetEvidence(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if vh.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "verification store unavailable"})
		return
	}
	rec, err := vh.store.Get(c.Request.Context(), id)
	if errors.Is(err, verify.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "verification not found"})
		return
	}
	if err != nil {
		slog.Error("get evidence verification", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load verification"})
		return
	}
	if !vh.authorizeVerificationRead(c, rec) {
		return
	}

	includeControls := c.Query("include_controls") == "true" || c.Query("include_controls") == "1"
	in := verify.EvidenceInput{
		Actor:           middleware.Actor(c),
		IncludeControls: includeControls,
	}
	if err := populateEvidenceFromRecord(&in, rec); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupt verification record"})
		return
	}
	vid := rec.ID
	in.VerificationID = &vid
	in.StepName = rec.StepName

	sign := len(vh.hmacKey) > 0
	if c.Query("sign") == "false" || c.Query("sign") == "0" {
		sign = false
	}
	if sign {
		in.HMACKey = vh.hmacKey
	}

	out, err := verify.BuildEvidence(in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// authorizeVerificationRead enforces session ACL. Missing session → master only;
// unauthorized callers always get 404 (no existence leak).
func (vh *VerifyHandler) authorizeVerificationRead(c *gin.Context, rec *verify.Record) bool {
	if rec.SessionID != nil {
		_, ok := vh.h.checkSessionAccess(c, *rec.SessionID, models.OrgRoleViewer)
		return ok
	}
	if middleware.Actor(c) == "master" {
		return true
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "verification not found"})
	return false
}

func populateEvidenceFromRecord(in *verify.EvidenceInput, rec *verify.Record) error {
	var spec verify.Spec
	var obs verify.Observation
	var checks []verify.Check
	if err := json.Unmarshal(rec.Spec, &spec); err != nil {
		return err
	}
	if err := json.Unmarshal(rec.Observation, &obs); err != nil {
		return err
	}
	if len(rec.Checks) > 0 {
		if err := json.Unmarshal(rec.Checks, &checks); err != nil {
			return err
		}
	}
	in.HasCheckpoint = true
	in.Spec = spec
	in.Observation = obs
	in.Result = verify.Result{Passed: rec.Passed, Checks: checks}
	in.SessionID = rec.SessionID
	in.RunID = rec.RunID
	return nil
}
