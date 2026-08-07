-- Post-run / post-step verification checkpoints (exit code, stdout, file exists)
CREATE TABLE IF NOT EXISTS run_verifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
    run_id UUID REFERENCES runs(id) ON DELETE SET NULL,
    mission_run_id UUID,
    step_name VARCHAR(200) NOT NULL DEFAULT '',
    spec JSONB NOT NULL DEFAULT '{}',
    observation JSONB NOT NULL DEFAULT '{}',
    passed BOOLEAN NOT NULL,
    checks JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_run_verifications_session ON run_verifications(session_id, created_at DESC);
CREATE INDEX idx_run_verifications_run ON run_verifications(run_id);
CREATE INDEX idx_run_verifications_mission_run ON run_verifications(mission_run_id)
    WHERE mission_run_id IS NOT NULL;
