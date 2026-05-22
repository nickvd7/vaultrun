-- v0.6: persistent workspace snapshots and cross-session artifact sharing

CREATE TABLE session_snapshots (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID        NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    created_by   TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    archive_path TEXT        NOT NULL,  -- absolute host path to the .tar.gz
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_snapshots_session_id ON session_snapshots(session_id);
CREATE INDEX idx_snapshots_created_by ON session_snapshots(created_by);

-- Shared artifacts: session files promoted to a cross-session registry.
-- Stored in {WORKSPACE_BASE_DIR}/artifacts/<id>/ on the host.
CREATE TABLE shared_artifacts (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT        NOT NULL,              -- original filename
    artifact_path TEXT       NOT NULL UNIQUE,       -- absolute host path
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    content_type TEXT        NOT NULL DEFAULT 'application/octet-stream',
    created_by   TEXT        NOT NULL,
    session_id   UUID        REFERENCES sessions(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_artifacts_created_by ON shared_artifacts(created_by);
CREATE INDEX idx_artifacts_session_id ON shared_artifacts(session_id);
