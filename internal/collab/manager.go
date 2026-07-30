package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

var (
	ErrAgentNotFound     = errors.New("agent not found")
	ErrSessionNotCollab  = errors.New("session does not allow collaboration")
	ErrMaxAgentsReached  = errors.New("maximum agents reached for session")
)

// Manager handles multi-agent collaboration
type Manager struct {
	db    *sqlx.DB
	redis *redis.Client
}

// New creates a new collaboration manager
func New(db *sqlx.DB, redisClient *redis.Client) *Manager {
	return &Manager{
		db:    db,
		redis: redisClient,
	}
}

// === Agent Presence ===

// JoinSession registers an agent as active in a session
func (m *Manager) JoinSession(ctx context.Context, sessionID uuid.UUID, agentID, agentName string) (*Agent, error) {
	// Check if session allows collaboration
	var allowCollab bool
	var maxAgents int
	err := m.db.GetContext(ctx, &allowCollab,
		"SELECT allow_collaboration FROM sessions WHERE id = $1", sessionID)
	if err != nil {
		return nil, fmt.Errorf("check collaboration: %w", err)
	}
	if !allowCollab {
		return nil, ErrSessionNotCollab
	}

	err = m.db.GetContext(ctx, &maxAgents,
		"SELECT COALESCE(max_agents, 1) FROM sessions WHERE id = $1", sessionID)
	if err != nil {
		return nil, fmt.Errorf("get max agents: %w", err)
	}

	// Check current agent count
	activeCount, err := m.redis.SCard(ctx, redisKeyActiveAgents(sessionID)).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get active count: %w", err)
	}

	if int(activeCount) >= maxAgents {
		return nil, ErrMaxAgentsReached
	}

	agent := &Agent{
		ID:           agentID,
		Name:         agentName,
		SessionID:    sessionID,
		Status:       AgentStatusActive,
		LastActivity: time.Now(),
		ConnectedAt:  time.Now(),
	}

	// Store in Redis (in-memory presence)
	agentJSON, err := json.Marshal(agent)
	if err != nil {
		return nil, fmt.Errorf("marshal agent: %w", err)
	}

	pipe := m.redis.Pipeline()
	pipe.SAdd(ctx, redisKeyActiveAgents(sessionID), agentID)
	pipe.Set(ctx, redisKeyAgent(sessionID, agentID), agentJSON, 24*time.Hour)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("redis store agent: %w", err)
	}

	// Store in DB (audit trail)
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO session_agents (session_id, agent_id, agent_name, status, connected_at, last_activity)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (session_id, agent_id) DO UPDATE SET
			status = $4,
			connected_at = $5,
			last_activity = $6,
			disconnected_at = NULL
	`, sessionID, agentID, agentName, AgentStatusActive, agent.ConnectedAt, agent.LastActivity)
	if err != nil {
		return nil, fmt.Errorf("db store agent: %w", err)
	}

	// Publish join event
	m.publishEvent(ctx, sessionID, WSMessage{
		Type: WSMessageTypeAgentJoined,
		Payload: AgentJoinedPayload{
			Agent: *agent,
		},
	})

	return agent, nil
}

// LeaveSession removes an agent from a session
func (m *Manager) LeaveSession(ctx context.Context, sessionID uuid.UUID, agentID string) error {
	// Remove from Redis
	pipe := m.redis.Pipeline()
	pipe.SRem(ctx, redisKeyActiveAgents(sessionID), agentID)
	pipe.Del(ctx, redisKeyAgent(sessionID, agentID))
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis remove agent: %w", err)
	}

	// Update DB
	_, err = m.db.ExecContext(ctx, `
		UPDATE session_agents
		SET status = $1, disconnected_at = $2
		WHERE session_id = $3 AND agent_id = $4
	`, AgentStatusDisconnected, time.Now(), sessionID, agentID)
	if err != nil {
		return fmt.Errorf("db update agent: %w", err)
	}

	// Publish leave event
	m.publishEvent(ctx, sessionID, WSMessage{
		Type: WSMessageTypeAgentLeft,
		Payload: AgentLeftPayload{
			AgentID:   agentID,
			Timestamp: time.Now(),
		},
	})

	return nil
}

// UpdatePresence updates an agent's presence (current file, status)
func (m *Manager) UpdatePresence(ctx context.Context, sessionID uuid.UUID, agentID string, currentFile string, status string) error {
	// Get current agent
	agentJSON, err := m.redis.Get(ctx, redisKeyAgent(sessionID, agentID)).Result()
	if err == redis.Nil {
		return ErrAgentNotFound
	}
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}

	var agent Agent
	if err := json.Unmarshal([]byte(agentJSON), &agent); err != nil {
		return fmt.Errorf("unmarshal agent: %w", err)
	}

	// Update
	agent.CurrentFile = currentFile
	agent.Status = status
	agent.LastActivity = time.Now()

	// Save back to Redis
	agentJSON2, err := json.Marshal(agent)
	if err != nil {
		return fmt.Errorf("marshal agent: %w", err)
	}

	err = m.redis.Set(ctx, redisKeyAgent(sessionID, agentID), agentJSON2, 24*time.Hour).Err()
	if err != nil {
		return fmt.Errorf("save agent: %w", err)
	}

	// Update DB
	_, err = m.db.ExecContext(ctx, `
		UPDATE session_agents
		SET current_file = $1, status = $2, last_activity = $3
		WHERE session_id = $4 AND agent_id = $5
	`, currentFile, status, agent.LastActivity, sessionID, agentID)
	if err != nil {
		return fmt.Errorf("db update presence: %w", err)
	}

	// Publish presence update
	m.publishEvent(ctx, sessionID, WSMessage{
		Type: WSMessageTypePresenceUpdate,
		Payload: PresenceUpdatePayload{
			AgentID:     agentID,
			Status:      status,
			CurrentFile: currentFile,
			Timestamp:   agent.LastActivity,
		},
	})

	return nil
}

// GetActiveAgents returns all active agents for a session
func (m *Manager) GetActiveAgents(ctx context.Context, sessionID uuid.UUID) ([]Agent, error) {
	// Get agent IDs from Redis
	agentIDs, err := m.redis.SMembers(ctx, redisKeyActiveAgents(sessionID)).Result()
	if err == redis.Nil {
		return []Agent{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent IDs: %w", err)
	}

	agents := make([]Agent, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		agentJSON, err := m.redis.Get(ctx, redisKeyAgent(sessionID, agentID)).Result()
		if err == redis.Nil {
			continue // Agent expired
		}
		if err != nil {
			return nil, fmt.Errorf("get agent %s: %w", agentID, err)
		}

		var agent Agent
		if err := json.Unmarshal([]byte(agentJSON), &agent); err != nil {
			return nil, fmt.Errorf("unmarshal agent: %w", err)
		}

		agents = append(agents, agent)
	}

	return agents, nil
}

// === Messaging ===

// SendMessage sends a message from one agent to another (or broadcast)
func (m *Manager) SendMessage(ctx context.Context, sessionID uuid.UUID, from, to, body string, msgType string) (*Message, error) {
	msg := &Message{
		ID:        uuid.New(),
		SessionID: sessionID,
		Type:      msgType,
		From:      from,
		To:        to,
		Body:      body,
		Timestamp: time.Now(),
	}

	// Store in DB
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO agent_messages (id, session_id, message_type, from_agent, to_agent, body, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, msg.ID, msg.SessionID, msg.Type, msg.From, msg.To, msg.Body, msg.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("store message: %w", err)
	}

	// Publish message event
	m.publishEvent(ctx, sessionID, WSMessage{
		Type: WSMessageTypeMessage,
		Payload: MessagePayload{
			Message: *msg,
		},
	})

	return msg, nil
}

// GetMessages retrieves recent messages for a session
func (m *Manager) GetMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]Message, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var rows []struct {
		ID          uuid.UUID `db:"id"`
		SessionID   uuid.UUID `db:"session_id"`
		MessageType string    `db:"message_type"`
		FromAgent   string    `db:"from_agent"`
		ToAgent     *string   `db:"to_agent"`
		Body        string    `db:"body"`
		CreatedAt   time.Time `db:"created_at"`
	}

	err := m.db.SelectContext(ctx, &rows, `
		SELECT id, session_id, message_type, from_agent, to_agent, body, created_at
		FROM agent_messages
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}

	messages := make([]Message, len(rows))
	for i, row := range rows {
		to := ""
		if row.ToAgent != nil {
			to = *row.ToAgent
		}
		messages[i] = Message{
			ID:        row.ID,
			SessionID: row.SessionID,
			Type:      row.MessageType,
			From:      row.FromAgent,
			To:        to,
			Body:      row.Body,
			Timestamp: row.CreatedAt,
		}
	}

	return messages, nil
}

// === File Versioning ===

// RecordFileChange records a file modification
func (m *Manager) RecordFileChange(ctx context.Context, sessionID uuid.UUID, filePath, agentID, changeType, checksum string, sizeBytes int64) (*FileChange, error) {
	// Get next version
	var version int
	err := m.db.GetContext(ctx, &version,
		"SELECT get_next_file_version($1, $2)", sessionID, filePath)
	if err != nil {
		return nil, fmt.Errorf("get next version: %w", err)
	}

	change := &FileChange{
		SessionID:  sessionID,
		File:       filePath,
		ChangedBy:  agentID,
		ChangeType: changeType,
		Version:    version,
		Timestamp:  time.Now(),
	}

	// Store in DB
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO file_versions (session_id, file_path, version, changed_by, change_type, checksum, size_bytes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, sessionID, filePath, version, agentID, changeType, checksum, sizeBytes, change.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("store file version: %w", err)
	}

	// Publish file change event
	m.publishEvent(ctx, sessionID, WSMessage{
		Type: WSMessageTypeFileChanged,
		Payload: FileChangedPayload{
			Change: *change,
		},
	})

	return change, nil
}

// GetFileVersion returns the current version of a file
func (m *Manager) GetFileVersion(ctx context.Context, sessionID uuid.UUID, filePath string) (int, error) {
	var version int
	err := m.db.GetContext(ctx, &version,
		"SELECT COALESCE(MAX(version), 0) FROM file_versions WHERE session_id = $1 AND file_path = $2",
		sessionID, filePath)
	if err != nil {
		return 0, fmt.Errorf("get file version: %w", err)
	}
	return version, nil
}

// === Event Publishing ===

func (m *Manager) publishEvent(ctx context.Context, sessionID uuid.UUID, msg WSMessage) {
	if m.redis == nil {
		return
	}

	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return // Best effort
	}

	_ = m.redis.Publish(ctx, redisKeyEvents(sessionID), msgJSON).Err()
}

// SubscribeToEvents subscribes to session events
func (m *Manager) SubscribeToEvents(ctx context.Context, sessionID uuid.UUID) *redis.PubSub {
	if m.redis == nil {
		return nil
	}
	return m.redis.Subscribe(ctx, redisKeyEvents(sessionID))
}

// === Redis Key Helpers ===

func redisKeyActiveAgents(sessionID uuid.UUID) string {
	return fmt.Sprintf("collab:session:%s:agents", sessionID)
}

func redisKeyAgent(sessionID uuid.UUID, agentID string) string {
	return fmt.Sprintf("collab:session:%s:agent:%s", sessionID, agentID)
}

func redisKeyEvents(sessionID uuid.UUID) string {
	return fmt.Sprintf("collab:session:%s:events", sessionID)
}
