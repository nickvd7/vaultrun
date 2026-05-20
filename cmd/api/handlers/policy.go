package handlers

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/policy"
)

type PolicyHandler struct{ h *Hub }

func NewPolicyHandler(h *Hub) *PolicyHandler { return &PolicyHandler{h: h} }

// GET /api/v1/policy
func (ph *PolicyHandler) Get(c *gin.Context) {
	filePath := ph.h.PolicyFile()
	if filePath == "" {
		c.JSON(http.StatusOK, gin.H{
			"enabled": false,
		})
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		// Log the real error (with path) server-side; never expose host paths
		// or OS error details to callers (M-3).
		slog.Error("policy file unreadable", "err", err)
		c.JSON(http.StatusOK, gin.H{
			"enabled": true,
			"error":   "policy file unreadable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled": true,
		"content": string(content),
	})
}

type evalRequest struct {
	Type      string   `json:"type" binding:"required"` // "command" or "file"
	SessionID string   `json:"session_id"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	Path      string   `json:"path"`
	Write     bool     `json:"write"`
}

// POST /api/v1/policy/eval — dry-run: evaluate a command or file access
// against the currently loaded policy without actually executing anything.
func (ph *PolicyHandler) Eval(c *gin.Context) {
	var req evalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Type != "command" && req.Type != "file" {
		c.JSON(http.StatusBadRequest, gin.H{"error": `type must be "command" or "file"`})
		return
	}

	// Parse optional session_id; fall back to a zero UUID.
	sessionID := uuid.UUID{}
	if req.SessionID != "" {
		parsed, err := uuid.Parse(req.SessionID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
			return
		}
		sessionID = parsed
	}

	// Use the request context so OPA evaluation is cancelled if the client
	// disconnects or the server shuts down (M-7).
	ctx := c.Request.Context()
	hook := ph.h.Policy()

	var d policy.Decision
	switch req.Type {
	case "command":
		if req.Command == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "command is required for type=command"})
			return
		}
		d = hook.EvalCommand(ctx, sessionID, req.Command, req.Args)
	case "file":
		if req.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is required for type=file"})
			return
		}
		d = hook.EvalFileAccess(ctx, sessionID, req.Path, req.Write)
	}

	resp := gin.H{
		"allowed": d.Allowed,
		"type":    req.Type,
	}
	if !d.Allowed && d.Reason != "" {
		resp["reason"] = d.Reason
	}
	c.JSON(http.StatusOK, resp)
}
