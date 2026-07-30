-- Rollback Session Replay feature

-- Remove audit event types (no-op, they're just constants)

-- Remove session columns
ALTER TABLE sessions DROP COLUMN IF EXISTS forked_from_checkpoint_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS replay_enabled;

-- Remove runs columns
DROP INDEX IF EXISTS idx_runs_checkpoint;
ALTER TABLE runs DROP COLUMN IF EXISTS restored_from_checkpoint_id;
ALTER TABLE runs DROP COLUMN IF EXISTS checkpoint_id;

-- Remove replay_checkpoints table
DROP INDEX IF EXISTS idx_replay_checkpoints_run;
DROP INDEX IF EXISTS idx_replay_checkpoints_created;
DROP INDEX IF EXISTS idx_replay_checkpoints_session;
DROP TABLE IF EXISTS replay_checkpoints;
