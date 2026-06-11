-- Enforce unique API-key names so that the actor identity (which equals the
-- key name) is unambiguous. Two keys with the same name would share access to
-- each other's sessions and audit logs.
ALTER TABLE api_keys ADD CONSTRAINT api_keys_name_unique UNIQUE (name);
