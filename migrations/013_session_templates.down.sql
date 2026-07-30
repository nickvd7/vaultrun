-- Drop template_id from sessions
DROP INDEX IF EXISTS idx_sessions_template;
ALTER TABLE sessions DROP COLUMN IF EXISTS template_id;

-- Drop trigger and function
DROP TRIGGER IF EXISTS trigger_increment_template_use_count ON template_usage;
DROP FUNCTION IF EXISTS increment_template_use_count();

-- Drop tables
DROP INDEX IF EXISTS idx_template_usage_created;
DROP INDEX IF EXISTS idx_template_usage_org;
DROP INDEX IF EXISTS idx_template_usage_session;
DROP INDEX IF EXISTS idx_template_usage_template;
DROP TABLE IF EXISTS template_usage;

DROP INDEX IF EXISTS idx_templates_search;
DROP INDEX IF EXISTS idx_templates_tags;
DROP INDEX IF EXISTS idx_templates_use_count;
DROP INDEX IF EXISTS idx_templates_featured;
DROP INDEX IF EXISTS idx_templates_published;
DROP INDEX IF EXISTS idx_templates_category;
DROP INDEX IF EXISTS idx_templates_slug;
DROP TABLE IF EXISTS session_templates;
