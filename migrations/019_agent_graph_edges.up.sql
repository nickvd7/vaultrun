-- Agent swarm graph: directed edges between agents in a collaborative session
CREATE TABLE IF NOT EXISTS agent_graph_edges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    from_agent VARCHAR(100) NOT NULL,
    to_agent VARCHAR(100) NOT NULL,
    relation VARCHAR(50) NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT agent_graph_edges_unique UNIQUE (session_id, from_agent, to_agent, relation),
    CONSTRAINT agent_graph_edges_no_self CHECK (from_agent <> to_agent)
);

CREATE INDEX idx_agent_graph_edges_session ON agent_graph_edges(session_id);
CREATE INDEX idx_agent_graph_edges_from ON agent_graph_edges(session_id, from_agent);
CREATE INDEX idx_agent_graph_edges_to ON agent_graph_edges(session_id, to_agent);
