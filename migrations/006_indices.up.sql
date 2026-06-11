-- v0.6 follow-up: covering and composite indices for quota + name lookups

-- Covering index for TotalArtifactBytes: SUM(size_bytes) WHERE created_by = $1
-- allows a pure index-only scan without touching the heap.
CREATE INDEX idx_artifacts_created_by_size ON shared_artifacts(created_by, size_bytes);

-- Composite index for snapshot name lookups within a session.
-- Supports fast duplicate-name detection and ORDER BY name queries.
CREATE INDEX idx_snapshots_session_name ON session_snapshots(session_id, name);
