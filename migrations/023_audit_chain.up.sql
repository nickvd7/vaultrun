-- Hash chain for the audit log. Each entry gets a sequence number and carries
-- the signature of the entry before it (both covered by its own HMAC), so a
-- deleted or reordered entry breaks the chain. audit_chain holds the chain
-- head and the moment chaining began, signed with the same key, so neither can
-- be changed by someone who can write to the database but lacks the key.
ALTER TABLE audit_logs ADD COLUMN seq BIGINT;
ALTER TABLE audit_logs ADD COLUMN prev_sig TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX audit_logs_seq_idx ON audit_logs (seq) WHERE seq IS NOT NULL;

CREATE TABLE audit_chain (
    id             SMALLINT PRIMARY KEY CHECK (id = 1),
    seq            BIGINT      NOT NULL,
    last_sig       TEXT        NOT NULL,
    pruned_through BIGINT      NOT NULL DEFAULT 0,
    started_at     TIMESTAMPTZ NOT NULL,
    mac            TEXT        NOT NULL
);
