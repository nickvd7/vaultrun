-- v0.3 multi-tenancy: organizations, RBAC, org-scoped sessions and API keys

CREATE TABLE organizations (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    slug        TEXT        NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_organizations_slug ON organizations (slug);

-- per-org membership with role-based access control
-- role: viewer (read-only) | executor (read+write) | admin (full)
CREATE TABLE org_members (
    org_id      UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    principal   TEXT        NOT NULL,
    role        TEXT        NOT NULL DEFAULT 'executor'
                            CHECK (role IN ('viewer', 'executor', 'admin')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, principal)
);

CREATE INDEX idx_org_members_principal ON org_members (principal);

-- api_keys can optionally belong to an org
ALTER TABLE api_keys ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE SET NULL;

-- sessions can optionally belong to an org (for sharing within the org)
ALTER TABLE sessions ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE SET NULL;

CREATE INDEX idx_sessions_org_id ON sessions (org_id) WHERE org_id IS NOT NULL;
