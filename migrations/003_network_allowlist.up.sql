-- Add per-session network allowlist.
-- TEXT[] matches the StringArray model type (same as the 'args' column on runs).
-- An empty array means no allowlist (full bridge access when network_enabled=true).
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS allowed_hosts TEXT[] NOT NULL DEFAULT '{}';
