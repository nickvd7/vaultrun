ALTER TABLE runs DROP COLUMN IF EXISTS callback_url;
DROP INDEX IF EXISTS idx_sessions_labels;
ALTER TABLE sessions DROP COLUMN IF EXISTS labels;
