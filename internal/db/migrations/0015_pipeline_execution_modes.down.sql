DROP INDEX IF EXISTS pipeline_jobs_isolated_reserved_idx;
DROP INDEX IF EXISTS pipeline_jobs_isolated_unreserved_idx;
DROP INDEX IF EXISTS pipeline_jobs_running_runner_idx;

ALTER TABLE pipeline_jobs
    DROP CONSTRAINT IF EXISTS pipeline_jobs_execution_pool_chk,
    DROP CONSTRAINT IF EXISTS pipeline_jobs_execution_mode_chk,
    DROP COLUMN IF EXISTS reserved_runner_id,
    DROP COLUMN IF EXISTS execution_pool,
    DROP COLUMN IF EXISTS execution_mode;
