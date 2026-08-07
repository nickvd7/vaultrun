-- Missions: reusable verified tool sequences (workflow-as-asset)
CREATE TABLE IF NOT EXISTS missions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    org_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    created_by VARCHAR(200) NOT NULL DEFAULT '',
    version VARCHAR(20) NOT NULL DEFAULT '1.0.0',
    published BOOLEAN NOT NULL DEFAULT false,
    use_count INTEGER NOT NULL DEFAULT 0,
    steps JSONB NOT NULL DEFAULT '[]',
    tags TEXT[] DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_missions_slug ON missions(slug);
CREATE INDEX idx_missions_org ON missions(org_id);
CREATE INDEX idx_missions_published ON missions(published);

CREATE TABLE IF NOT EXISTS mission_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mission_id UUID NOT NULL REFERENCES missions(id) ON DELETE CASCADE,
    session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
    org_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    status VARCHAR(40) NOT NULL DEFAULT 'pending',
    step_results JSONB NOT NULL DEFAULT '[]',
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX idx_mission_runs_mission ON mission_runs(mission_id);
CREATE INDEX idx_mission_runs_session ON mission_runs(session_id);

CREATE OR REPLACE FUNCTION increment_mission_use_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE missions SET use_count = use_count + 1 WHERE id = NEW.mission_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_increment_mission_use_count
AFTER INSERT ON mission_runs
FOR EACH ROW EXECUTE FUNCTION increment_mission_use_count();
