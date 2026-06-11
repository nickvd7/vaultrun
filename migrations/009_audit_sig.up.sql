-- Add per-entry HMAC integrity signature to audit_logs.
-- When AUDIT_HMAC_KEY is set, each entry's sig field contains an HMAC-SHA256
-- hex digest over its immutable fields, enabling tamper detection after the fact.
ALTER TABLE audit_logs ADD COLUMN sig TEXT NOT NULL DEFAULT '';
