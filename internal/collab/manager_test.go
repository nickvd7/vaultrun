package collab

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAgentTypes(t *testing.T) {
	sessionID := uuid.New()
	agent := Agent{
		ID:           "agent_a",
		Name:         "Architect",
		SessionID:    sessionID,
		Status:       AgentStatusActive,
		CurrentFile:  "main.go",
		LastActivity: time.Now(),
		ConnectedAt:  time.Now(),
	}

	if agent.ID != "agent_a" {
		t.Errorf("expected agent ID 'agent_a', got %s", agent.ID)
	}
	if agent.Status != AgentStatusActive {
		t.Errorf("expected status 'active', got %s", agent.Status)
	}
}

func TestMessageTypes(t *testing.T) {
	sessionID := uuid.New()
	msg := Message{
		ID:        uuid.New(),
		SessionID: sessionID,
		Type:      MessageTypeDirect,
		From:      "agent_a",
		To:        "agent_b",
		Body:      "Hello!",
		Timestamp: time.Now(),
	}

	if msg.Type != MessageTypeDirect {
		t.Errorf("expected type 'direct', got %s", msg.Type)
	}
	if msg.From != "agent_a" {
		t.Errorf("expected from 'agent_a', got %s", msg.From)
	}
	if msg.To != "agent_b" {
		t.Errorf("expected to 'agent_b', got %s", msg.To)
	}
}

func TestFileChange(t *testing.T) {
	sessionID := uuid.New()
	change := FileChange{
		SessionID:  sessionID,
		File:       "main.go",
		ChangedBy:  "agent_a",
		ChangeType: ChangeTypeModified,
		Version:    2,
		Timestamp:  time.Now(),
	}

	if change.ChangeType != ChangeTypeModified {
		t.Errorf("expected change type 'modified', got %s", change.ChangeType)
	}
	if change.Version != 2 {
		t.Errorf("expected version 2, got %d", change.Version)
	}
}

func TestWSMessage(t *testing.T) {
	agent := Agent{
		ID:     "agent_a",
		Name:   "Architect",
		Status: AgentStatusActive,
	}

	msg := WSMessage{
		Type: WSMessageTypeAgentJoined,
		Payload: AgentJoinedPayload{
			Agent: agent,
		},
	}

	if msg.Type != WSMessageTypeAgentJoined {
		t.Errorf("expected type 'agent_joined', got %s", msg.Type)
	}

	payload, ok := msg.Payload.(AgentJoinedPayload)
	if !ok {
		t.Fatal("payload is not AgentJoinedPayload")
	}

	if payload.Agent.ID != "agent_a" {
		t.Errorf("expected agent ID 'agent_a', got %s", payload.Agent.ID)
	}
}

func TestAgentStatusConstants(t *testing.T) {
	if AgentStatusActive != "active" {
		t.Errorf("expected 'active', got %s", AgentStatusActive)
	}
	if AgentStatusIdle != "idle" {
		t.Errorf("expected 'idle', got %s", AgentStatusIdle)
	}
	if AgentStatusDisconnected != "disconnected" {
		t.Errorf("expected 'disconnected', got %s", AgentStatusDisconnected)
	}
}

func TestMessageTypeConstants(t *testing.T) {
	if MessageTypeDirect != "direct" {
		t.Errorf("expected 'direct', got %s", MessageTypeDirect)
	}
	if MessageTypeBroadcast != "broadcast" {
		t.Errorf("expected 'broadcast', got %s", MessageTypeBroadcast)
	}
	if MessageTypeSystem != "system" {
		t.Errorf("expected 'system', got %s", MessageTypeSystem)
	}
}

func TestChangeTypeConstants(t *testing.T) {
	if ChangeTypeCreated != "created" {
		t.Errorf("expected 'created', got %s", ChangeTypeCreated)
	}
	if ChangeTypeModified != "modified" {
		t.Errorf("expected 'modified', got %s", ChangeTypeModified)
	}
	if ChangeTypeDeleted != "deleted" {
		t.Errorf("expected 'deleted', got %s", ChangeTypeDeleted)
	}
}

func TestWSMessageTypeConstants(t *testing.T) {
	if WSMessageTypeAgentJoined != "agent_joined" {
		t.Errorf("expected 'agent_joined', got %s", WSMessageTypeAgentJoined)
	}
	if WSMessageTypeAgentLeft != "agent_left" {
		t.Errorf("expected 'agent_left', got %s", WSMessageTypeAgentLeft)
	}
	if WSMessageTypeMessage != "message" {
		t.Errorf("expected 'message', got %s", WSMessageTypeMessage)
	}
	if WSMessageTypeFileChanged != "file_changed" {
		t.Errorf("expected 'file_changed', got %s", WSMessageTypeFileChanged)
	}
	if WSMessageTypePresenceUpdate != "presence_update" {
		t.Errorf("expected 'presence_update', got %s", WSMessageTypePresenceUpdate)
	}
}

func TestRedisKeyHelpers(t *testing.T) {
	sessionID := uuid.MustParse("12345678-1234-1234-1234-123456789012")
	agentID := "agent_a"

	activeKey := redisKeyActiveAgents(sessionID)
	expectedActive := "collab:session:12345678-1234-1234-1234-123456789012:agents"
	if activeKey != expectedActive {
		t.Errorf("expected %s, got %s", expectedActive, activeKey)
	}

	agentKey := redisKeyAgent(sessionID, agentID)
	expectedAgent := "collab:session:12345678-1234-1234-1234-123456789012:agent:agent_a"
	if agentKey != expectedAgent {
		t.Errorf("expected %s, got %s", expectedAgent, agentKey)
	}

	eventsKey := redisKeyEvents(sessionID)
	expectedEvents := "collab:session:12345678-1234-1234-1234-123456789012:events"
	if eventsKey != expectedEvents {
		t.Errorf("expected %s, got %s", expectedEvents, eventsKey)
	}
}

// Integration tests would require Redis + DB
// They are commented out to avoid CI failures

/*
func TestManagerJoinSession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	
	redisClient := setupTestRedis(t)
	defer redisClient.Close()
	
	m := New(db, redisClient)
	ctx := context.Background()
	
	sessionID := createTestSession(t, db, true, 4) // allow_collaboration=true, max_agents=4
	
	agent, err := m.JoinSession(ctx, sessionID, "agent_a", "Architect")
	if err != nil {
		t.Fatalf("join session: %v", err)
	}
	
	if agent.ID != "agent_a" {
		t.Errorf("expected agent ID 'agent_a', got %s", agent.ID)
	}
	if agent.Status != AgentStatusActive {
		t.Errorf("expected status 'active', got %s", agent.Status)
	}
}

func TestManagerGetActiveAgents(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	
	redisClient := setupTestRedis(t)
	defer redisClient.Close()
	
	m := New(db, redisClient)
	ctx := context.Background()
	
	sessionID := createTestSession(t, db, true, 4)
	
	// Join multiple agents
	m.JoinSession(ctx, sessionID, "agent_a", "Architect")
	m.JoinSession(ctx, sessionID, "agent_b", "Developer")
	
	agents, err := m.GetActiveAgents(ctx, sessionID)
	if err != nil {
		t.Fatalf("get active agents: %v", err)
	}
	
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestManagerSendMessage(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	
	redisClient := setupTestRedis(t)
	defer redisClient.Close()
	
	m := New(db, redisClient)
	ctx := context.Background()
	
	sessionID := createTestSession(t, db, true, 4)
	
	msg, err := m.SendMessage(ctx, sessionID, "agent_a", "agent_b", "Hello!", MessageTypeDirect)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	
	if msg.From != "agent_a" {
		t.Errorf("expected from 'agent_a', got %s", msg.From)
	}
	if msg.To != "agent_b" {
		t.Errorf("expected to 'agent_b', got %s", msg.To)
	}
}
*/
