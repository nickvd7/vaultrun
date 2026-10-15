-- Sessions whose container creation failed were left with stopped_at NULL and
-- kept counting against the per-actor active-session limit forever.
UPDATE sessions SET stopped_at = updated_at WHERE status = 'error' AND stopped_at IS NULL;
