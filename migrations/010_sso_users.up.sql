-- SSO / federated identity: maps an external identity (OIDC sub or SAML NameID)
-- to a VaultRun API key. One row per (provider, external_id) pair.
CREATE TABLE IF NOT EXISTS sso_users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT        NOT NULL,
    name          TEXT,
    provider      TEXT        NOT NULL,  -- 'oidc' or 'saml'
    external_id   TEXT        NOT NULL,  -- OIDC sub claim or SAML NameID
    api_key_id    UUID        REFERENCES api_keys(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(provider, external_id)
);

CREATE INDEX IF NOT EXISTS sso_users_email_idx ON sso_users(email);
