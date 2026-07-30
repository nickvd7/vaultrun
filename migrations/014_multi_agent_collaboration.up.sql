-- Multi-Agent Collaboration

-- Active agents in sessions (in-memory state backed by Redis, this table is for audit/history)
CREATE TABLE IF NOT EXISTS session_agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    agent_id VARCHAR(100) NOT NULL,
    agent_name VARCHAR(200) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    current_file TEXT,
    last_activity TIMESTAMP NOT NULL DEFAULT NOW(),
    connected_at TIMESTAMP NOT NULL DEFAULT NOW(),
    disconnected_at TIMESTAMP,
    
    UNIQUE(session_id, agent_id)
);

CREATE INDEX idx_session_agents_session ON session_agents(session_id);
CREATE INDEX idx_session_agents_status ON session_agents(status);
CREATE INDEX idx_session_agents_activity ON session_agents(last_activity);

-- Agent-to-agent messages
CREATE TABLE IF NOT EXISTS agent_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_type VARCHAR(50) NOT NULL, -- 'direct', 'broadcast', 'system'
    from_agent VARCHAR(100) NOT NULL,
    to_agent VARCHAR(100), -- NULL for broadcast
    body TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_messages_session ON agent_messages(session_id, created_at DESC);
CREATE INDEX idx_agent_messages_to ON agent_messages(to_agent, created_at DESC);
CREATE INDEX idx_agent_messages_from ON agent_messages(from_agent, created_at DESC);

-- File version tracking (for conflict detection)
CREATE TABLE IF NOT EXISTS file_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    changed_by VARCHAR(100) NOT NULL, -- Agent ID
    change_type VARCHAR(50) NOT NULL, -- 'created', 'modified', 'deleted'
    checksum VARCHAR(64), -- SHA256 of file content
    size_bytes BIGINT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    UNIQUE(session_id, file_path, version)
);

CREATE INDEX idx_file_versions_session ON file_versions(session_id, file_path);
CREATE INDEX idx_file_versions_agent ON file_versions(changed_by);
CREATE INDEX idx_file_versions_created ON file_versions(created_at);

-- Get current version for a file (helper view)
CREATE OR REPLACE VIEW current_file_versions AS
SELECT DISTINCT ON (session_id, file_path)
    session_id,
    file_path,
    version,
    changed_by,
    change_type,
    checksum,
    created_at
FROM file_versions
ORDER BY session_id, file_path, version DESC;

-- Function to get next version number for a file
CREATE OR REPLACE FUNCTION get_next_file_version(p_session_id UUID, p_file_path TEXT)
RETURNS INTEGER AS $$
DECLARE
    v_current_version INTEGER;
BEGIN
    SELECT COALESCE(MAX(version), 0) INTO v_current_version
    FROM file_versions
    WHERE session_id = p_session_id AND file_path = p_file_path;
    
    RETURN v_current_version + 1;
END;
$$ LANGUAGE plpgsql;

-- Add collaboration metadata to sessions
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS max_agents INTEGER DEFAULT 1;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS allow_collaboration BOOLEAN DEFAULT false;

-- Indexes for collaboration queries
CREATE INDEX idx_sessions_collaboration ON sessions(allow_collaboration) WHERE allow_collaboration = true;
