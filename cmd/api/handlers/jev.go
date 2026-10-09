package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/jev"
	"github.com/nickvd7/vaultrun/internal/missions"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/verify"
)

// JevHandler exposes opt-in TypeSafe Jev gate endpoints.
type JevHandler struct {
	h       *Hub
	store   *verify.Store
	missions *missions.Manager
	client  *jev.Client // nil when disabled / misconfigured
}

// NewJevHandler constructs a handler. Client is loaded when VAULTRUN_JEV_ENABLED=true.
func NewJevHandler(h *Hub, store *verify.Store, missionsMgr *missions.Manager) *JevHandler {
	jh := &JevHandler{h: h, store: store, missions: missionsMgr}
	if os.Getenv("VAULTRUN_JEV_ENABLED") != "true" {
		return jh
	}
	c, err := jev.ConfigFromEnv(os.Getenv)
	if err != nil {
		slog.Warn("VAULTRUN_JEV_ENABLED=true but Jev client not configured", "err", err)
		return jh
	}
	jh.client = c
	slog.Info("jev gate enabled", "provider", c.Provider, "base_url", c.BaseURL, "model", c.Model)
	return jh
}

func (jh *JevHandler) enabled() bool { return jh != nil && jh.client != nil }

type jevGateRequest struct {
	Claim            string              `json:"claim" binding:"required"`
	Evidence         string              `json:"evidence"`
	VerificationID   *uuid.UUID          `json:"verification_id"`
	Task             string              `json:"task"`
	MinNoul          *float64            `json:"min_noul"`
	OnFail           string              `json:"on_fail"`
	IncludeControls  bool                `json:"include_controls"`
	MissionID        *uuid.UUID          `json:"mission_id"`
	StepIndex        *int                `json:"step_index"`
}

// Gate POST /api/v1/verify/jev-gate
func (jh *JevHandler) Gate(c *gin.Context) {
	if !jh.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "jev not enabled — set VAULTRUN_JEV_ENABLED=true and OPENJEV_API_KEY or TYPESAFE_API_KEY"})
		return
	}
	var req jevGateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if utf8.RuneCountInString(req.Claim) > jev.MaxClaimRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "claim too long"})
		return
	}

	in := jev.GateInput{
		Claim:  req.Claim,
		Task:   req.Task,
		OnFail: jev.ParseOnFail(req.OnFail),
	}
	if req.MinNoul != nil {
		in.MinNoul = *req.MinNoul
	}

	if req.MissionID != nil && jh.missions != nil {
		stepCfg, err := jh.missionStepVerify(c, *req.MissionID, req.StepIndex)
		if err != nil {
			return // response written
		}
		if stepCfg != nil {
			if in.MinNoul == 0 && stepCfg.JevMinNoul > 0 {
				in.MinNoul = stepCfg.JevMinNoul
			}
			if req.OnFail == "" && stepCfg.JevOnFail != "" {
				in.OnFail = jev.ParseOnFail(stepCfg.JevOnFail)
			}
			if in.Task == "" {
				// leave task; step description applied by caller if needed
			}
			if strings.TrimSpace(req.Claim) == "" && stepCfg.JevClaim != "" {
				in.Claim = stepCfg.JevClaim
			}
		}
	}

	evidence, err := jh.resolveEvidence(c, req.Evidence, req.VerificationID, req.IncludeControls)
	if err != nil {
		return
	}
	in.Evidence = evidence

	res, err := jh.client.GateClaim(c.Request.Context(), in)
	if err != nil {
		slog.Error("jev gate", "err", err, "actor", middleware.Actor(c))
		c.JSON(http.StatusBadGateway, gin.H{"error": "jev gate failed"})
		return
	}
	status := http.StatusOK
	if !res.Passed && res.Action == string(jev.OnFailFail) {
		status = http.StatusConflict // deterministic fail for automation
	}
	c.JSON(status, res)
}

type jevVerifyRequest struct {
	Claims           []string   `json:"claims"`
	Claim            string     `json:"claim"`
	Evidence         string     `json:"evidence"`
	VerificationID   *uuid.UUID `json:"verification_id"`
	IncludeControls  bool       `json:"include_controls"`
}

// VerifyClaims POST /api/v1/verify/jev
func (jh *JevHandler) VerifyClaims(c *gin.Context) {
	if !jh.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "jev not enabled — set VAULTRUN_JEV_ENABLED=true and OPENJEV_API_KEY or TYPESAFE_API_KEY"})
		return
	}
	var req jevVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	claims := req.Claims
	if len(claims) == 0 && req.Claim != "" {
		claims = []string{req.Claim}
	}
	if len(claims) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "claim or claims required"})
		return
	}
	evidence, err := jh.resolveEvidence(c, req.Evidence, req.VerificationID, req.IncludeControls)
	if err != nil {
		return
	}
	resp, err := jh.client.VerifyClaims(c.Request.Context(), evidence, claims)
	if err != nil {
		slog.Error("jev verify", "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "jev verify failed"})
		return
	}
	c.JSON(http.StatusOK, resp)
}

type missionStepVerifyRequest struct {
	Claim           string              `json:"claim"`
	Evidence        string              `json:"evidence"`
	VerificationID  *uuid.UUID          `json:"verification_id"`
	IncludeControls bool                `json:"include_controls"`
	Observation     *verify.Observation `json:"observation"`
	SessionID       *uuid.UUID          `json:"session_id"`
	RunID           *uuid.UUID          `json:"run_id"`
	StepIndex       int                 `json:"step_index"`
}

// MissionStepVerify POST /api/v1/missions/:id/steps/verify
// Evaluates deterministic step.verify checks and optional Jev gate (fail/hold).
func (jh *JevHandler) MissionStepVerify(c *gin.Context) {
	missionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if jh.missions == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "missions unavailable"})
		return
	}
	m, err := jh.missions.Get(c.Request.Context(), missionID)
	if errors.Is(err, missions.ErrMissionNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get failed"})
		return
	}
	actor := middleware.Actor(c)
	// Reuse mission write ACL for step verification (executors).
	mh := &MissionHandler{manager: jh.missions, hub: jh.h}
	if !mh.mayWrite(c, actor, m) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req missionStepVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.StepIndex < 0 || req.StepIndex >= len(m.Steps) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "step_index out of range"})
		return
	}
	step := m.Steps[req.StepIndex]
	sv := step.Verify
	if sv == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "step has no verify config"})
		return
	}

	out := gin.H{
		"mission_id": missionID,
		"step_index": req.StepIndex,
		"step_name":  step.Name,
		"actor":      actor,
	}

	// Deterministic checks when present.
	hasDet := sv.ExitCodeZero != nil || sv.StdoutContains != "" || sv.FileExists != ""
	if hasDet {
		obs := verify.Observation{}
		if req.Observation != nil {
			obs = *req.Observation
		}
		spec := verify.Spec{
			ExitCodeZero:   sv.ExitCodeZero,
			StdoutContains: sv.StdoutContains,
			FileExists:     sv.FileExists,
		}
		var probe verify.FileProbe
		if req.SessionID != nil && sv.FileExists != "" {
			if _, ok := jh.h.checkSessionAccess(c, *req.SessionID, models.OrgRoleViewer); !ok {
				return
			}
			sid := *req.SessionID
			probe = func(path string) (bool, error) { return jh.h.ws.Exists(sid, path) }
		}
		result := verify.Evaluate(spec, obs, probe)
		out["checkpoint"] = result
		if !result.Passed {
			out["action"] = "fail"
			out["passed"] = false
			out["summary"] = "deterministic verify failed"
			c.JSON(http.StatusConflict, out)
			return
		}
	}

	needsJev := strings.TrimSpace(sv.JevClaim) != "" || strings.TrimSpace(req.Claim) != ""
	if !needsJev {
		out["action"] = "pass"
		out["passed"] = true
		out["summary"] = "deterministic verify passed (no jev claim)"
		c.JSON(http.StatusOK, out)
		return
	}
	if !jh.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "step requires jev but VAULTRUN_JEV_ENABLED/TYPESAFE_API_KEY not configured"})
		return
	}

	claim := strings.TrimSpace(req.Claim)
	if claim == "" {
		claim = sv.JevClaim
	}
	evidence, err := jh.resolveEvidence(c, req.Evidence, req.VerificationID, req.IncludeControls)
	if err != nil {
		return
	}
	// If no evidence provided, build a minimal observation evidence blob.
	if evidence == "" && req.Observation != nil {
		raw, _ := json.Marshal(map[string]any{
			"observation": req.Observation,
			"checkpoint":  out["checkpoint"],
			"step":        step.Name,
		})
		evidence = string(raw)
	}
	if evidence == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "evidence, verification_id, or observation required for jev gate"})
		return
	}

	gate, err := jh.client.GateClaim(c.Request.Context(), jev.GateInput{
		Claim:    claim,
		Evidence: evidence,
		Task:     step.Description,
		MinNoul:  sv.JevMinNoul,
		OnFail:   jev.ParseOnFail(sv.JevOnFail),
	})
	if err != nil {
		slog.Error("mission step jev gate", "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "jev gate failed"})
		return
	}
	out["jev"] = gate
	out["action"] = gate.Action
	out["passed"] = gate.Passed
	out["summary"] = gate.Summary
	status := http.StatusOK
	if !gate.Passed && gate.Action == string(jev.OnFailFail) {
		status = http.StatusConflict
	}
	// hold → 202 Accepted so automation can pause without treating as hard fail
	if !gate.Passed && gate.Action == string(jev.OnFailHold) {
		status = http.StatusAccepted
	}
	c.JSON(status, out)
}

func (jh *JevHandler) resolveEvidence(c *gin.Context, inline string, verificationID *uuid.UUID, includeControls bool) (string, error) {
	if strings.TrimSpace(inline) != "" {
		if len(inline) > jev.MaxStateBytes*2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "evidence too large"})
			return "", errors.New("evidence too large")
		}
		return inline, nil
	}
	if verificationID == nil {
		return "", nil
	}
	if jh.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "verification store unavailable"})
		return "", errors.New("no store")
	}
	rec, err := jh.store.Get(c.Request.Context(), *verificationID)
	if errors.Is(err, verify.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "verification not found"})
		return "", err
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load verification"})
		return "", err
	}
	vh := &VerifyHandler{h: jh.h, store: jh.store}
	if !vh.authorizeVerificationRead(c, rec) {
		return "", errors.New("denied")
	}
	in := verify.EvidenceInput{
		Actor:           middleware.Actor(c),
		IncludeControls: includeControls,
	}
	if err := populateEvidenceFromRecord(&in, rec); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupt verification record"})
		return "", err
	}
	vid := rec.ID
	in.VerificationID = &vid
	in.StepName = rec.StepName
	ev, err := verify.BuildEvidence(in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", err
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal evidence"})
		return "", err
	}
	return string(raw), nil
}

func (jh *JevHandler) missionStepVerify(c *gin.Context, missionID uuid.UUID, stepIndex *int) (*missions.StepVerify, error) {
	m, err := jh.missions.Get(c.Request.Context(), missionID)
	if errors.Is(err, missions.ErrMissionNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "mission not found"})
		return nil, err
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mission load failed"})
		return nil, err
	}
	actor := middleware.Actor(c)
	mh := &MissionHandler{manager: jh.missions, hub: jh.h}
	if !mh.mayRead(c, actor, m) {
		c.JSON(http.StatusNotFound, gin.H{"error": "mission not found"})
		return nil, errors.New("denied")
	}
	idx := 0
	if stepIndex != nil {
		idx = *stepIndex
	}
	if idx < 0 || idx >= len(m.Steps) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "step_index out of range"})
		return nil, errors.New("range")
	}
	return m.Steps[idx].Verify, nil
}
