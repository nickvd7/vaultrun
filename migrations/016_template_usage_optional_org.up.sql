-- Template usage: allow recording a use that has no organization.
--
-- A session created from a template by an API key that belongs to no org is a
-- legitimate personal session. With org_id NOT NULL the usage insert failed,
-- which silently dropped the row and left session_templates.use_count wrong
-- (the counter is driven by a trigger on this table).
ALTER TABLE template_usage ALTER COLUMN org_id DROP NOT NULL;
