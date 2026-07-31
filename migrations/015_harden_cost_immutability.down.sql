-- Rollback migration 015: restore the update-only immutability trigger

DROP TRIGGER IF EXISTS replay_checkpoints_immutable ON replay_checkpoints;
DROP FUNCTION IF EXISTS prevent_checkpoint_update();

DROP TRIGGER IF EXISTS cost_metrics_immutable ON cost_metrics;
DROP FUNCTION IF EXISTS prevent_cost_metric_change();

CREATE OR REPLACE FUNCTION prevent_cost_metric_update()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'cost_metrics records are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cost_metrics_immutable
    BEFORE UPDATE ON cost_metrics
    FOR EACH ROW
    EXECUTE FUNCTION prevent_cost_metric_update();
