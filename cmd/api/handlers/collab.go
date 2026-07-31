package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/collab"
	"github.com/nickvd7/vaultrun/internal/models"
)

// CollabHandler handles collaboration-related HTTP requests
type CollabHandler struct {
	manager  *collab.Manager
	hub      *collab.Hub
	baseHub  *Hub
	upgrader websocket.Upgrader
}

// newUpgrader builds a WebSocket upgrader that only accepts the origins the
// deployment configured through CORS_ALLOWED_ORIGINS.
//
// The default CheckOrigin already rejects cross-origin requests, but it does so
// by comparing against the Host header, which is unreliable behind a proxy.
// Accepting every origin — the previous behaviour — makes the endpoint reachable
// from any page a user visits: the browser attaches their cookies, so a
// malicious site could drive a session on their behalf.
//
// A request without an Origin header is allowed: non-browser clients (the SDK,
// an agent) do not send one, and they are the primary consumers here. Browsers
// always send it for WebSocket handshakes, so this does not weaken the check
// for the case it exists to defend.
func newUpgrader(allowedOrigins []string) websocket.Upgrader {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[strings.ToLower(strings.TrimSpace(origin))] = true
	}

	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // non-browser client
			}
			if allowed["*"] {
				return true
			}
			return allowed[strings.ToLower(origin)]
		},
	}
}

// NewCollabHandler creates a new collaboration handler
func NewCollabHandler(manager *collab.Manager, hub *collab.Hub, baseHub *Hub) *CollabHandler {
	return &CollabHandler{
		upgrader: newUpgrader(baseHub.cfg.Server.CORSOrigins),
		manager:  manager,
		hub:     hub,
		baseHub: baseHub,
	}
}

// WebSocket handles WebSocket connections for collaborative sessions
// GET /api/v1/sessions/:id/ws?agent_id=agent_a&agent_name=Architect
func (h *CollabHandler) WebSocket(c *gin.Context) {
	sessionIDStr := c.Param("id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session ID"})
		return
	}

	agentID := c.Query("agent_id")
	if err := collab.ValidateAgentID(agentID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	agentName := c.Query("agent_name")
	if agentName == "" {
		agentName = agentID
	}
	if err := collab.ValidateAgentName(agentName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check session access
	actor := middleware.Actor(c)
	session, ok := h.baseHub.checkSessionAccess(c, sessionID, "viewer")
	if !ok {
		return
	}

	// Check if collaboration is allowed
	var allowCollab bool
	err = h.baseHub.db.GetContext(c.Request.Context(), &allowCollab,
		"SELECT COALESCE(allow_collaboration, false) FROM sessions WHERE id = $1", sessionID)
	if err != nil || !allowCollab {
		c.JSON(http.StatusForbidden, gin.H{"error": "collaboration not enabled for this session"})
		return
	}

	// Join the session before upgrading: once the connection is hijacked we can
	// no longer write an HTTP error response, so every rejection must happen here.
	agent, err := h.manager.JoinSession(c.Request.Context(), sessionID, agentID, agentName)
	switch {
	case err == collab.ErrMaxAgentsReached:
		c.JSON(http.StatusConflict, gin.H{"error": "maximum agents reached for this session"})
		return
	case err == collab.ErrSessionNotCollab:
		c.JSON(http.StatusForbidden, gin.H{"error": "collaboration not enabled"})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade already wrote an HTTP error. Release the slot we just took,
		// otherwise a client that fails to upgrade leaks it until the TTL expires.
		_ = h.manager.LeaveSession(c.Request.Context(), sessionID, agentID)
		return
	}

	// Create WebSocket client
	client := collab.NewClient(sessionID, agentID, agentName, conn, h.manager)
	h.hub.RegisterClient(client)

	// Audit log
	h.baseHub.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &sessionID,
		Action:    models.ActionAgentJoined,
		Metadata: map[string]interface{}{
			"agent_id":   agentID,
			"agent_name": agentName,
		},
	})

	// Start client pumps in goroutines
	go client.WritePump(c.Request.Context())
	go client.ReadPump(c.Request.Context())

	// Send welcome message with current session state
	agents, _ := h.manager.GetActiveAgents(c.Request.Context(), sessionID)
	welcomeMsg := collab.WSMessage{
		Type: "welcome",
		Payload: map[string]interface{}{
			"agent":         agent,
			"active_agents": agents,
			"session":       session,
		},
	}
	welcomeMsgJSON, _ := json.Marshal(welcomeMsg)
	client.Send(welcomeMsgJSON)
}

// GetActiveAgents returns all active agents for a session
// GET /api/v1/sessions/:id/agents
func (h *CollabHandler) GetActiveAgents(c *gin.Context) {
	sessionIDStr := c.Param("id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session ID"})
		return
	}

	// Check session access
	_, ok := h.baseHub.checkSessionAccess(c, sessionID, "viewer")
	if !ok {
		return
	}

	agents, err := h.manager.GetActiveAgents(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

// GetMessages returns recent messages for a session
// GET /api/v1/sessions/:id/messages?limit=100
func (h *CollabHandler) GetMessages(c *gin.Context) {
	sessionIDStr := c.Param("id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session ID"})
		return
	}

	// Check session access
	_, ok := h.baseHub.checkSessionAccess(c, sessionID, "viewer")
	if !ok {
		return
	}

	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}

	messages, err := h.manager.GetMessages(c.Request.Context(), sessionID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

// SendMessage sends a message to agents in a session
// POST /api/v1/sessions/:id/messages
func (h *CollabHandler) SendMessage(c *gin.Context) {
	sessionIDStr := c.Param("id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session ID"})
		return
	}

	// Check session access
	_, ok := h.baseHub.checkSessionAccess(c, sessionID, "viewer")
	if !ok {
		return
	}

	var req struct {
		From string `json:"from" binding:"required"`
		To   string `json:"to"`
		Body string `json:"body" binding:"required"`
		Type string `json:"type"` // "direct" or "broadcast"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	msgType := req.Type
	if msgType == "" {
		if req.To == "" {
			msgType = collab.MessageTypeBroadcast
		} else {
			msgType = collab.MessageTypeDirect
		}
	}

	message, err := h.manager.SendMessage(c.Request.Context(), sessionID, req.From, req.To, req.Body, msgType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, message)
}

// EnableCollaboration enables collaboration for a session
// POST /api/v1/sessions/:id/enable-collaboration
func (h *CollabHandler) EnableCollaboration(c *gin.Context) {
	sessionIDStr := c.Param("id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session ID"})
		return
	}

	// Check session access (must be owner or admin)
	_, ok := h.baseHub.checkSessionAccess(c, sessionID, "admin")
	if !ok {
		return
	}

	var req struct {
		MaxAgents int `json:"max_agents"` // Default 4
	}
	_ = c.ShouldBindJSON(&req)

	maxAgents := req.MaxAgents
	if maxAgents == 0 {
		maxAgents = 4 // sensible default when the caller omits the field
	}
	if err := collab.ValidateMaxAgents(maxAgents); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err = h.baseHub.db.ExecContext(c.Request.Context(),
		"UPDATE sessions SET allow_collaboration = true, max_agents = $1 WHERE id = $2",
		maxAgents, sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "collaboration enabled",
		"max_agents": maxAgents,
	})
}
