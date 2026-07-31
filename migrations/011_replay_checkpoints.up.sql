-- Session Replay: checkpoint every command execution for time-travel debugging
-- See: docs/features/session-replay.md

CREATE TABLE replay_checkpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id UUID REFERENCES runs(id) ON DELETE SET NULL,
    
    -- Checkpoint metadata
    checkpoint_number INT NOT NULL,  -- 1, 2, 3, ... within session
    name VARCHAR(255),                -- Optional user label
    description TEXT,                 -- Auto-generated or user-provided
    
    -- Snapshot references
    workspace_snapshot_id UUID NOT NULL,  -- Snapshot UUID (not FK to allow independent lifecycle)
    archive_path TEXT NOT NULL,           -- Path to workspace snapshot archive
    env_vars_snapshot JSONB,              -- Environment variables at this point
    
    -- Execution context
    command TEXT,                     -- The command that was run
    args JSONB,                       -- Command arguments array
    exit_code INT,
    duration_ms INT,
    stdout_preview TEXT,              -- First 500 chars
    stderr_preview TEXT,              -- First 500 chars
    
    -- Security: HMAC signature for integrity protection
    signature VARCHAR(128) NOT NULL,
    
    -- Metadata
    created_at TIMESTAMPTZ DEFAULT NOW(),
    size_bytes BIGINT,                -- Workspace snapshot size
    
    UNIQUE(session_id, checkpoint_number)
);

CREATE INDEX idx_replay_checkpoints_session ON replay_checkpoints(session_id, checkpoint_number DESC);
CREATE INDEX idx_replay_checkpoints_created ON replay_checkpoints(created_at DESC);
CREATE INDEX idx_replay_checkpoints_run ON replay_checkpoints(run_id) WHERE run_id IS NOT NULL;

-- Add replay tracking to runs table
ALTER TABLE runs ADD COLUMN checkpoint_id UUID REFERENCES replay_checkpoints(id) ON DELETE SET NULL;
ALTER TABLE runs ADD COLUMN restored_from_checkpoint_id UUID REFERENCES replay_checkpoints(id) ON DELETE SET NULL;

CREATE INDEX idx_runs_checkpoint ON runs(checkpoint_id) WHERE checkpoint_id IS NOT NULL;

-- Add replay configuration to sessions
ALTER TABLE sessions ADD COLUMN replay_enabled BOOLEAN DEFAULT FALSE;
ALTER TABLE sessions ADD COLUMN forked_from_checkpoint_id UUID REFERENCES replay_checkpoints(id) ON DELETE SET NULL;

-- Audit events for replay operations
-- (Using existing audit_logs table, just documenting new action types)
-- ActionCheckpointCreated = "checkpoint.created"
-- ActionCheckpointRestored = "checkpoint.restored"
-- ActionCheckpointForked = "checkpoint.forked"
-- ActionCheckpointDeleted = "checkpoint.deleted"
