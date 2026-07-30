package collab

import (
	"time"

	"github.com/google/uuid"
)

// Agent represents a connected agent in a collaborative session
type Agent struct {
	ID           string    `json:"id"`           // Agent identifier (e.g., "agent_a")
	Name         string    `json:"name"`         // Human-readable name
	SessionID    uuid.UUID `json:"session_id"`   // Session this agent is connected to
	Status       string    `json:"status"`       // "active", "idle", "disconnected"
	CurrentFile  string    `json:"current_file"` // File currently being edited (nullable)
	LastActivity time.Time `json:"last_activity"`
	ConnectedAt  time.Time `json:"connected_at"`
}

// Message represents a message between agents
type Message struct {
	ID        uuid.UUID `json:"id"`
	SessionID uuid.UUID `json:"session_id"`
	Type      string    `json:"type"` // "direct", "broadcast", "system"
	From      string    `json:"from"` // Agent ID
	To        string    `json:"to"`   // Agent ID (empty for broadcast)
	Body      string    `json:"body"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// FileChange represents a file modification event
type FileChange struct {
	SessionID   uuid.UUID `json:"session_id"`
	File        string    `json:"file"`
	ChangedBy   string    `json:"changed_by"` // Agent ID
	ChangeType  string    `json:"change_type"` // "created", "modified", "deleted"
	Version     int       `json:"version"`     // Incremental version number
	Timestamp   time.Time `json:"timestamp"`
}

// WSMessage is the wrapper for all WebSocket messages
type WSMessage struct {
	Type    string      `json:"type"` // "agent_joined", "agent_left", "message", "file_changed", "presence_update"
	Payload interface{} `json:"payload"`
}

// AgentJoinedPayload is sent when an agent joins
type AgentJoinedPayload struct {
	Agent Agent `json:"agent"`
}

// AgentLeftPayload is sent when an agent disconnects
type AgentLeftPayload struct {
	AgentID   string    `json:"agent_id"`
	Timestamp time.Time `json:"timestamp"`
}

// MessagePayload wraps a message
type MessagePayload struct {
	Message Message `json:"message"`
}

// FileChangedPayload is sent when a file is modified
type FileChangedPayload struct {
	Change FileChange `json:"change"`
}

// PresenceUpdatePayload is sent when agent presence changes
type PresenceUpdatePayload struct {
	AgentID     string    `json:"agent_id"`
	Status      string    `json:"status"`
	CurrentFile string    `json:"current_file"`
	Timestamp   time.Time `json:"timestamp"`
}

// AgentStatus constants
const (
	AgentStatusActive       = "active"
	AgentStatusIdle         = "idle"
	AgentStatusDisconnected = "disconnected"
)

// MessageType constants
const (
	MessageTypeDirect    = "direct"
	MessageTypeBroadcast = "broadcast"
	MessageTypeSystem    = "system"
)

// ChangeType constants
const (
	ChangeTypeCreated  = "created"
	ChangeTypeModified = "modified"
	ChangeTypeDeleted  = "deleted"
)

// WSMessageType constants
const (
	WSMessageTypeAgentJoined     = "agent_joined"
	WSMessageTypeAgentLeft       = "agent_left"
	WSMessageTypeMessage         = "message"
	WSMessageTypeFileChanged     = "file_changed"
	WSMessageTypePresenceUpdate  = "presence_update"
	WSMessageTypePing            = "ping"
	WSMessageTypePong            = "pong"
)
