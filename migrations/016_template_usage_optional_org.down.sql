-- Restore the NOT NULL constraint on template_usage.org_id.
-- Rows recorded for org-less sessions are removed first: they cannot satisfy
-- the constraint and there is no org to attribute them to.
DELETE FROM template_usage WHERE org_id IS NULL;
ALTER TABLE template_usage ALTER COLUMN org_id SET NOT NULL;
