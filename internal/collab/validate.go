package collab

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// ErrInvalidInput wraps every validation failure in this package so that HTTP
// handlers can answer 400 instead of turning a caller's mistake into a 500.
var ErrInvalidInput = errors.New("invalid input")

// invalid builds a validation error that satisfies errors.Is(err, ErrInvalidInput).
func invalid(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

// Limits on agent-supplied values. Agent IDs become part of Redis key names and
// a Postgres unique key, so they are the most tightly constrained.
const (
	MaxAgentIDLength   = 64
	MaxAgentNameLength = 200
	MaxMessageBytes    = 64 * 1024 // 64 KB
	MaxFilePathLength  = 4096
	MaxAgentsPerSession = 32
)

// agentIDPattern restricts agent IDs to characters that are safe inside a
// colon-delimited Redis key. Allowing ':' would let one agent's ID collide with
// a different key in the collab:session:<id>:… namespace.
var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_\-.]*$`)

// ValidateAgentID checks an agent identifier before it is used in a Redis key
// or stored as part of the (session_id, agent_id) unique constraint.
func ValidateAgentID(agentID string) error {
	if agentID == "" {
		return invalid("agent_id must not be empty")
	}
	if len(agentID) > MaxAgentIDLength {
		return invalid("agent_id is %d characters, maximum is %d", len(agentID), MaxAgentIDLength)
	}
	if !agentIDPattern.MatchString(agentID) {
		return invalid("agent_id %q must start with an alphanumeric and contain only letters, digits, '_', '-' and '.'", agentID)
	}
	return nil
}

// ValidateAgentName checks the human-readable agent name. Names are only ever
// displayed, so the constraint is length and valid UTF-8 rather than charset.
func ValidateAgentName(name string) error {
	if name == "" {
		return invalid("agent_name must not be empty")
	}
	if len(name) > MaxAgentNameLength {
		return invalid("agent_name is %d bytes, maximum is %d", len(name), MaxAgentNameLength)
	}
	if !utf8.ValidString(name) {
		return invalid("agent_name is not valid UTF-8")
	}
	return nil
}

// ValidateMessageBody bounds a message before it is written to Postgres and
// broadcast to every connected agent. Without a bound, one agent can exhaust
// the message table and saturate every peer's send buffer.
func ValidateMessageBody(body string) error {
	if body == "" {
		return invalid("message body must not be empty")
	}
	if len(body) > MaxMessageBytes {
		return invalid("message body is %d bytes, maximum is %d", len(body), MaxMessageBytes)
	}
	if !utf8.ValidString(body) {
		return invalid("message body is not valid UTF-8")
	}
	return nil
}

// ValidateMessageType rejects unknown types so that a client cannot invent a
// type that downstream consumers mishandle.
func ValidateMessageType(msgType string) error {
	switch msgType {
	case MessageTypeDirect, MessageTypeBroadcast, MessageTypeSystem:
		return nil
	default:
		return invalid("message type %q must be one of %q, %q, %q",
			msgType, MessageTypeDirect, MessageTypeBroadcast, MessageTypeSystem)
	}
}

// ValidateFilePath bounds the presence "current file" field. The value is
// informational — it is never opened — so only length and encoding matter.
func ValidateFilePath(path string) error {
	if len(path) > MaxFilePathLength {
		return invalid("file path is %d bytes, maximum is %d", len(path), MaxFilePathLength)
	}
	if !utf8.ValidString(path) {
		return invalid("file path is not valid UTF-8")
	}
	return nil
}

// ValidateAgentStatus rejects unknown presence states.
func ValidateAgentStatus(status string) error {
	switch status {
	case AgentStatusActive, AgentStatusIdle, AgentStatusDisconnected:
		return nil
	default:
		return invalid("status %q must be one of %q, %q, %q",
			status, AgentStatusActive, AgentStatusIdle, AgentStatusDisconnected)
	}
}

// ValidateMaxAgents bounds the per-session agent cap. Each agent holds a
// WebSocket connection and two goroutines, so an unbounded value is a
// resource-exhaustion vector.
func ValidateMaxAgents(maxAgents int) error {
	if maxAgents < 1 {
		return invalid("max_agents must be at least 1, got %d", maxAgents)
	}
	if maxAgents > MaxAgentsPerSession {
		return invalid("max_agents is %d, maximum is %d", maxAgents, MaxAgentsPerSession)
	}
	return nil
}

// ValidateGraphRelation restricts swarm edge kinds.
func ValidateGraphRelation(relation string) error {
	switch relation {
	case RelationReportsTo, RelationReviews, RelationHandoff, RelationPeer:
		return nil
	default:
		return invalid("relation %q must be one of %q, %q, %q, %q",
			relation, RelationReportsTo, RelationReviews, RelationHandoff, RelationPeer)
	}
}
