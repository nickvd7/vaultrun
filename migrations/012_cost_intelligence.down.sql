-- Rollback cost intelligence

ALTER TABLE sessions DROP COLUMN IF EXISTS total_cost;
ALTER TABLE sessions DROP COLUMN IF EXISTS last_cost_update;

DROP TABLE IF EXISTS cost_alerts CASCADE;
DROP TABLE IF EXISTS cost_budgets CASCADE;
DROP TABLE IF EXISTS cost_metrics CASCADE;
DROP TABLE IF EXISTS cost_rates CASCADE;

DROP FUNCTION IF EXISTS prevent_cost_metric_update() CASCADE;
