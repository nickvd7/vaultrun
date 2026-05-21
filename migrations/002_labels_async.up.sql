-- Session labels: free-form key-value metadata for grouping/filtering.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS labels JSONB NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_sessions_labels ON sessions USING gin (labels);

-- Async runs: optional webhook callback URL stored with the run record.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS callback_url TEXT;
