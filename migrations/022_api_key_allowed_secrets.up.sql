-- NULL = key may request any secret (behaviour before this migration);
-- an array restricts the key to exactly those secret names ('{}' = none).
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS allowed_secrets TEXT[];
