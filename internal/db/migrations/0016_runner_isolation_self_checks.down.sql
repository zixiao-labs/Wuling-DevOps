DROP TABLE IF EXISTS runner_self_checks;
DROP INDEX IF EXISTS runners_isolated_job_id_uq;
ALTER TABLE runners DROP COLUMN IF EXISTS isolated_job_id;
