-- Migration 007: add revoked_at timestamp to api_keys
--
-- Adds a nullable revoked_at column so that revoking a key records *when* it
-- was revoked rather than just toggling active=false. This makes the audit
-- trail for key lifecycle events complete and allows operators to query which
-- keys were revoked within a time window.

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

-- Index to quickly find recently revoked keys.
CREATE INDEX IF NOT EXISTS idx_api_keys_revoked_at ON api_keys (revoked_at) WHERE revoked_at IS NOT NULL;
