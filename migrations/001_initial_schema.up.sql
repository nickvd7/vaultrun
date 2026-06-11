CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE api_keys (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    key_hash    TEXT        NOT NULL UNIQUE,
    prefix      TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ,
    active      BOOLEAN     NOT NULL DEFAULT TRUE
);

CREATE INDEX idx_api_keys_key_hash ON api_keys (key_hash);

CREATE TABLE sessions (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT,
    image             TEXT        NOT NULL DEFAULT 'python:3.12-slim',
    status            TEXT        NOT NULL DEFAULT 'created',
    container_id      TEXT,
    network_enabled   BOOLEAN     NOT NULL DEFAULT FALSE,
    cpu_limit         REAL        NOT NULL DEFAULT 1.0,
    memory_limit_mb   INTEGER     NOT NULL DEFAULT 512,
    timeout_seconds   INTEGER     NOT NULL DEFAULT 300,
    workspace_path    TEXT        NOT NULL,
    created_by        TEXT        NOT NULL DEFAULT 'system',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    stopped_at        TIMESTAMPTZ
);

CREATE INDEX idx_sessions_status     ON sessions (status);
CREATE INDEX idx_sessions_created_by ON sessions (created_by);
CREATE INDEX idx_sessions_created_at ON sessions (created_at DESC);

CREATE TABLE runs (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id       UUID        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    command          TEXT        NOT NULL,
    args             TEXT[]      NOT NULL DEFAULT '{}',
    env              JSONB       NOT NULL DEFAULT '{}',
    working_dir      TEXT        NOT NULL DEFAULT '/workspace',
    status           TEXT        NOT NULL DEFAULT 'pending',
    exit_code        INTEGER,
    stdout           TEXT,
    stderr           TEXT,
    duration_ms      BIGINT,
    timeout_seconds  INTEGER     NOT NULL DEFAULT 30,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);

CREATE INDEX idx_runs_session_id ON runs (session_id);
CREATE INDEX idx_runs_status     ON runs (status);
CREATE INDEX idx_runs_created_at ON runs (created_at DESC);

CREATE TABLE files (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    path         TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    content_type TEXT        NOT NULL DEFAULT 'application/octet-stream',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, path)
);

CREATE INDEX idx_files_session_id ON files (session_id);

CREATE TABLE audit_logs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor       TEXT        NOT NULL,
    session_id  UUID        REFERENCES sessions (id) ON DELETE SET NULL,
    run_id      UUID        REFERENCES runs (id) ON DELETE SET NULL,
    action      TEXT        NOT NULL,
    metadata    JSONB       NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_audit_logs_timestamp  ON audit_logs (timestamp DESC);
CREATE INDEX idx_audit_logs_session_id ON audit_logs (session_id);
CREATE INDEX idx_audit_logs_action     ON audit_logs (action);
CREATE INDEX idx_audit_logs_actor      ON audit_logs (actor);
