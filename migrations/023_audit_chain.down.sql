DROP TABLE IF EXISTS audit_chain;
DROP INDEX IF EXISTS audit_logs_seq_idx;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS prev_sig;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS seq;
