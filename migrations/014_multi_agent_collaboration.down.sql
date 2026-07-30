-- Drop collaboration features

-- Drop indexes
DROP INDEX IF EXISTS idx_sessions_collaboration;

-- Drop session columns
ALTER TABLE sessions DROP COLUMN IF EXISTS allow_collaboration;
ALTER TABLE sessions DROP COLUMN IF EXISTS max_agents;

-- Drop function
DROP FUNCTION IF EXISTS get_next_file_version(UUID, TEXT);

-- Drop view
DROP VIEW IF EXISTS current_file_versions;

-- Drop indexes
DROP INDEX IF EXISTS idx_file_versions_created;
DROP INDEX IF EXISTS idx_file_versions_agent;
DROP INDEX IF EXISTS idx_file_versions_session;
DROP TABLE IF EXISTS file_versions;

DROP INDEX IF EXISTS idx_agent_messages_from;
DROP INDEX IF EXISTS idx_agent_messages_to;
DROP INDEX IF EXISTS idx_agent_messages_session;
DROP TABLE IF EXISTS agent_messages;

DROP INDEX IF EXISTS idx_session_agents_activity;
DROP INDEX IF EXISTS idx_session_agents_status;
DROP INDEX IF EXISTS idx_session_agents_session;
DROP TABLE IF EXISTS session_agents;
