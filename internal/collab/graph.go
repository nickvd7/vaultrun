package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Graph edge relation kinds for swarm topology.
const (
	RelationReportsTo = "reports_to"
	RelationReviews   = "reviews"
	RelationHandoff   = "handoff"
	RelationPeer      = "peer"
)

// ErrGraphEdgeNotFound is returned when an edge cannot be deleted.
var ErrGraphEdgeNotFound = errors.New("graph edge not found")

// ErrGraphEdgeLimit is returned when a session has too many edges.
var ErrGraphEdgeLimit = errors.New("graph edge limit reached")

const (
	maxGraphLabelBytes    = 512
	maxGraphMetadataBytes = 16 * 1024
	maxGraphEdgesPerSession = 256
)

// GraphEdge is a directed relation between two agents in a session.
type GraphEdge struct {
	ID        uuid.UUID              `db:"id" json:"id"`
	SessionID uuid.UUID              `db:"session_id" json:"session_id"`
	FromAgent string                 `db:"from_agent" json:"from_agent"`
	ToAgent   string                 `db:"to_agent" json:"to_agent"`
	Relation  string                 `db:"relation" json:"relation"`
	Label     string                 `db:"label" json:"label"`
	Metadata  map[string]interface{} `db:"-" json:"metadata,omitempty"`
	CreatedAt time.Time              `db:"created_at" json:"created_at"`
}

// AgentGraph is the swarm topology snapshot for a session.
type AgentGraph struct {
	SessionID uuid.UUID   `json:"session_id"`
	Agents    []Agent     `json:"agents"`
	Edges     []GraphEdge `json:"edges"`
}

// AddGraphEdge upserts a directed edge in the session swarm graph.
func (m *Manager) AddGraphEdge(ctx context.Context, sessionID uuid.UUID, from, to, relation, label string, metadata map[string]interface{}) (*GraphEdge, error) {
	if err := ValidateAgentID(from); err != nil {
		return nil, err
	}
	if err := ValidateAgentID(to); err != nil {
		return nil, err
	}
	if err := ValidateGraphRelation(relation); err != nil {
		return nil, err
	}
	if from == to {
		return nil, invalid("from_agent and to_agent must differ")
	}
	if len(label) > maxGraphLabelBytes {
		return nil, invalid("label exceeds %d bytes", maxGraphLabelBytes)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	if len(metaJSON) > maxGraphMetadataBytes {
		return nil, invalid("metadata exceeds %d bytes", maxGraphMetadataBytes)
	}

	// Prefer active agents when Redis is available; fall back to session_agents history.
	fromOK, err := m.agentKnown(ctx, sessionID, from)
	if err != nil {
		return nil, err
	}
	toOK, err := m.agentKnown(ctx, sessionID, to)
	if err != nil {
		return nil, err
	}
	if !fromOK || !toOK {
		return nil, invalid("from_agent and to_agent must be known agents in the session")
	}

	var edgeCount int
	if err := m.db.GetContext(ctx, &edgeCount,
		`SELECT COUNT(*) FROM agent_graph_edges WHERE session_id = $1`, sessionID); err != nil {
		return nil, fmt.Errorf("count graph edges: %w", err)
	}
	var existing int
	_ = m.db.GetContext(ctx, &existing, `
		SELECT COUNT(*) FROM agent_graph_edges
		WHERE session_id=$1 AND from_agent=$2 AND to_agent=$3 AND relation=$4`,
		sessionID, from, to, relation)
	if existing == 0 && edgeCount >= maxGraphEdgesPerSession {
		return nil, ErrGraphEdgeLimit
	}

	id := uuid.New()
	now := time.Now().UTC()
	edge := &GraphEdge{
		ID:        id,
		SessionID: sessionID,
		FromAgent: from,
		ToAgent:   to,
		Relation:  relation,
		Label:     label,
		Metadata:  metadata,
		CreatedAt: now,
	}

	err = m.db.QueryRowContext(ctx, `
		INSERT INTO agent_graph_edges (id, session_id, from_agent, to_agent, relation, label, metadata, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (session_id, from_agent, to_agent, relation)
		DO UPDATE SET label = EXCLUDED.label, metadata = EXCLUDED.metadata
		RETURNING id, created_at`,
		id, sessionID, from, to, relation, label, metaJSON, now,
	).Scan(&edge.ID, &edge.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert graph edge: %w", err)
	}
	return edge, nil
}

func (m *Manager) agentKnown(ctx context.Context, sessionID uuid.UUID, agentID string) (bool, error) {
	if m.redis != nil {
		active, err := m.IsAgentActive(ctx, sessionID, agentID)
		if err != nil {
			return false, err
		}
		if active {
			return true, nil
		}
	}
	var n int
	err := m.db.GetContext(ctx, &n, `
		SELECT COUNT(*) FROM session_agents WHERE session_id=$1 AND agent_id=$2`, sessionID, agentID)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RemoveGraphEdge deletes an edge by ID within a session.
func (m *Manager) RemoveGraphEdge(ctx context.Context, sessionID, edgeID uuid.UUID) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM agent_graph_edges WHERE id = $1 AND session_id = $2`, edgeID, sessionID)
	if err != nil {
		return fmt.Errorf("delete graph edge: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrGraphEdgeNotFound
	}
	return nil
}

// ListGraphEdges returns all edges for a session.
func (m *Manager) ListGraphEdges(ctx context.Context, sessionID uuid.UUID) ([]GraphEdge, error) {
	rows, err := m.db.QueryxContext(ctx, `
		SELECT id, session_id, from_agent, to_agent, relation, label, metadata, created_at
		FROM agent_graph_edges WHERE session_id = $1 ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list graph edges: %w", err)
	}
	defer rows.Close()

	var edges []GraphEdge
	for rows.Next() {
		var e GraphEdge
		var metaRaw []byte
		if err := rows.Scan(&e.ID, &e.SessionID, &e.FromAgent, &e.ToAgent, &e.Relation, &e.Label, &metaRaw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &e.Metadata)
		}
		edges = append(edges, e)
	}
	if edges == nil {
		edges = []GraphEdge{}
	}
	return edges, rows.Err()
}

// GetAgentGraph returns active agents plus topology edges.
func (m *Manager) GetAgentGraph(ctx context.Context, sessionID uuid.UUID) (*AgentGraph, error) {
	agents, err := m.GetActiveAgents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	edges, err := m.ListGraphEdges(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return &AgentGraph{SessionID: sessionID, Agents: agents, Edges: edges}, nil
}
