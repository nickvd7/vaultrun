DROP INDEX IF EXISTS idx_api_keys_revoked_at;
ALTER TABLE api_keys DROP COLUMN IF EXISTS revoked_at;
