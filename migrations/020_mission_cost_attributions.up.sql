-- Mission cost attribution snapshots (cost_metrics stay immutable; link at run finish)
CREATE TABLE IF NOT EXISTS mission_cost_attributions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mission_id UUID NOT NULL REFERENCES missions(id) ON DELETE CASCADE,
    mission_run_id UUID NOT NULL REFERENCES mission_runs(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    compute_cost DECIMAL(10, 4) NOT NULL DEFAULT 0,
    storage_cost DECIMAL(10, 4) NOT NULL DEFAULT 0,
    network_cost DECIMAL(10, 4) NOT NULL DEFAULT 0,
    total_cost DECIMAL(10, 4) NOT NULL DEFAULT 0,
    metric_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (mission_run_id)
);

CREATE INDEX idx_mission_cost_attributions_mission ON mission_cost_attributions(mission_id);
CREATE INDEX idx_mission_cost_attributions_session ON mission_cost_attributions(session_id);
