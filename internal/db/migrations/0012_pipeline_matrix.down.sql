-- Reverse of 0012_pipeline_matrix. Drop the index and constraint first, then
-- the columns in reverse order of addition.
DROP INDEX IF EXISTS pipeline_jobs_group_idx;
ALTER TABLE pipeline_jobs
    DROP CONSTRAINT IF EXISTS pipeline_jobs_job_key_chk,
    DROP COLUMN IF EXISTS ordinal,
    DROP COLUMN IF EXISTS max_parallel,
    DROP COLUMN IF EXISTS fail_fast,
    DROP COLUMN IF EXISTS matrix,
    DROP COLUMN IF EXISTS job_key;
