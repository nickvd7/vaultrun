-- Migration 015: extend cost_metrics immutability to deletes
--
-- Migration 012 created a BEFORE UPDATE trigger only, so a billing record could
-- still be removed with DELETE. An append-only audit trail that permits
-- deletion is not append-only: the cheapest way to hide usage is to erase the
-- row rather than edit it.
--
-- Rows must still disappear when their session is deleted, which happens via
-- the ON DELETE CASCADE on cost_metrics.session_id. A statement-level guard
-- would block that too, so the trigger distinguishes the two cases: a cascaded
-- delete runs while the parent session row is already gone, whereas a direct
-- DELETE runs while it still exists.

CREATE OR REPLACE FUNCTION prevent_cost_metric_change()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- Allow the delete only when the owning session no longer exists,
        -- i.e. when this is the CASCADE from sessions.
        IF EXISTS (SELECT 1 FROM sessions WHERE id = OLD.session_id) THEN
            RAISE EXCEPTION 'cost_metrics records are immutable and cannot be deleted';
        END IF;
        RETURN OLD;
    END IF;

    RAISE EXCEPTION 'cost_metrics records are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS cost_metrics_immutable ON cost_metrics;

CREATE TRIGGER cost_metrics_immutable
    BEFORE UPDATE OR DELETE ON cost_metrics
    FOR EACH ROW
    EXECUTE FUNCTION prevent_cost_metric_change();

-- The old single-purpose function is no longer referenced.
DROP FUNCTION IF EXISTS prevent_cost_metric_update();

-- Replay checkpoints are the other tamper-evident record. The HMAC signature
-- detects modification after the fact, but there is no reason for the
-- application to ever update a checkpoint row, so block it at the database too.
CREATE OR REPLACE FUNCTION prevent_checkpoint_update()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'replay_checkpoints records are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS replay_checkpoints_immutable ON replay_checkpoints;

CREATE TRIGGER replay_checkpoints_immutable
    BEFORE UPDATE ON replay_checkpoints
    FOR EACH ROW
    EXECUTE FUNCTION prevent_checkpoint_update();
