DROP TRIGGER IF EXISTS trigger_increment_mission_use_count ON mission_runs;
DROP FUNCTION IF EXISTS increment_mission_use_count();
DROP TABLE IF EXISTS mission_runs;
DROP TABLE IF EXISTS missions;
