package collab

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Client represents a WebSocket client (agent connection)
type Client struct {
	SessionID uuid.UUID
	AgentID   string
	AgentName string
	Conn      *websocket.Conn
	Manager   *Manager
	send      chan []byte
	mu        sync.Mutex
}

// NewClient creates a new WebSocket client
func NewClient(sessionID uuid.UUID, agentID, agentName string, conn *websocket.Conn, manager *Manager) *Client {
	return &Client{
		SessionID: sessionID,
		AgentID:   agentID,
		AgentName: agentName,
		Conn:      conn,
		Manager:   manager,
		send:      make(chan []byte, 256),
	}
}

// ReadPump reads messages from the WebSocket connection
func (c *Client) ReadPump(ctx context.Context) {
	defer func() {
		c.Manager.LeaveSession(ctx, c.SessionID, c.AgentID)
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("websocket read error", "err", err, "agent", c.AgentID)
			}
			break
		}

		// Parse incoming message
		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			slog.Warn("invalid ws message", "err", err)
			continue
		}

		// Handle message
		c.handleMessage(ctx, msg)
	}
}

// WritePump sends messages to the WebSocket connection
func (c *Client) WritePump(ctx context.Context) {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Channel closed
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

// Send queues a message to be sent to the client
func (c *Client) Send(message []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case c.send <- message:
	default:
		// Channel full, drop message
		slog.Warn("ws send buffer full, dropping message", "agent", c.AgentID)
	}
}

// handleMessage processes incoming WebSocket messages
func (c *Client) handleMessage(ctx context.Context, msg WSMessage) {
	switch msg.Type {
	case WSMessageTypePing:
		// Respond with pong
		c.sendWSMessage(WSMessage{Type: WSMessageTypePong})

	case WSMessageTypePresenceUpdate:
		// Update agent presence
		payload, ok := msg.Payload.(map[string]interface{})
		if !ok {
			return
		}

		currentFile, _ := payload["current_file"].(string)
		status, _ := payload["status"].(string)
		if status == "" {
			status = AgentStatusActive
		}

		err := c.Manager.UpdatePresence(ctx, c.SessionID, c.AgentID, currentFile, status)
		if err != nil {
			slog.Error("update presence failed", "err", err)
		}

	case WSMessageTypeMessage:
		// Send message to other agents
		payload, ok := msg.Payload.(map[string]interface{})
		if !ok {
			return
		}

		body, _ := payload["body"].(string)
		to, _ := payload["to"].(string)
		msgType := MessageTypeBroadcast
		if to != "" {
			msgType = MessageTypeDirect
		}

		_, err := c.Manager.SendMessage(ctx, c.SessionID, c.AgentID, to, body, msgType)
		if err != nil {
			slog.Error("send message failed", "err", err)
		}

	default:
		slog.Warn("unknown ws message type", "type", msg.Type)
	}
}

// sendWSMessage sends a message to the client
func (c *Client) sendWSMessage(msg WSMessage) {
	msgJSON, err := json.Marshal(msg)
	if err != nil {
		slog.Error("marshal ws message failed", "err", err)
		return
	}
	c.Send(msgJSON)
}

// Hub manages all active WebSocket connections
type Hub struct {
	Manager  *Manager
	clients  map[uuid.UUID]map[string]*Client // sessionID -> agentID -> client
	register chan *Client
	unregister chan *Client
	mu       sync.RWMutex
}

// NewHub creates a new WebSocket hub
func NewHub(manager *Manager) *Hub {
	return &Hub{
		Manager:    manager,
		clients:    make(map[uuid.UUID]map[string]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run starts the hub
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if _, ok := h.clients[client.SessionID]; !ok {
				h.clients[client.SessionID] = make(map[string]*Client)
			}
			h.clients[client.SessionID][client.AgentID] = client
			h.mu.Unlock()

			slog.Info("agent connected", "session", client.SessionID, "agent", client.AgentID)

			// Start subscribing to Redis events for this session
			go h.subscribeToSession(ctx, client.SessionID)

		case client := <-h.unregister:
			h.mu.Lock()
			if sessionClients, ok := h.clients[client.SessionID]; ok {
				if _, ok := sessionClients[client.AgentID]; ok {
					delete(sessionClients, client.AgentID)
					close(client.send)

					if len(sessionClients) == 0 {
						delete(h.clients, client.SessionID)
					}
				}
			}
			h.mu.Unlock()

			slog.Info("agent disconnected", "session", client.SessionID, "agent", client.AgentID)

		case <-ctx.Done():
			return
		}
	}
}

// RegisterClient registers a new WebSocket client
func (h *Hub) RegisterClient(client *Client) {
	h.register <- client
}

// UnregisterClient unregisters a WebSocket client
func (h *Hub) UnregisterClient(client *Client) {
	h.unregister <- client
}

// subscribeToSession subscribes to Redis pub/sub for a session and broadcasts events
func (h *Hub) subscribeToSession(ctx context.Context, sessionID uuid.UUID) {
	pubsub := h.Manager.SubscribeToEvents(ctx, sessionID)
	if pubsub == nil {
		return
	}
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case msg := <-ch:
			// Broadcast to all clients in this session
			h.mu.RLock()
			sessionClients := h.clients[sessionID]
			h.mu.RUnlock()

			for _, client := range sessionClients {
				client.Send([]byte(msg.Payload))
			}

		case <-ctx.Done():
			return
		}
	}
}

// BroadcastToSession sends a message to all clients in a session
func (h *Hub) BroadcastToSession(sessionID uuid.UUID, message []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if sessionClients, ok := h.clients[sessionID]; ok {
		for _, client := range sessionClients {
			client.Send(message)
		}
	}
}
