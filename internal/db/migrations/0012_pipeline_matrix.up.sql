-- 0012_pipeline_matrix: GitHub-Actions-compatible `strategy.matrix` on a job.
--
-- Expansion happens at run creation: one pipeline_jobs row per matrix
-- combination, each carrying its own interpolated `definition` snapshot. The
-- dispatcher therefore keeps routing purely on row columns (runs_on /
-- resource_tier) and needs no notion of a matrix — but `needs` can no longer
-- resolve by NAME, because the display name now carries the combination suffix
-- ("build (ubuntu, 18)"). job_key holds the logical YAML job id, shared by
-- every leg, and is what `needs` resolves against.
--
-- fail_fast / max_parallel are denormalized onto every leg so the fail-fast
-- sibling cancel and the max-parallel admission gate stay single-table
-- predicates on the dispatch hot path (no JSONB digging, no join).
--
-- ordinal fixes a pre-existing ordering bug: every job in a run shares one
-- queued_at (Postgres now() is transaction-start time), so `ORDER BY
-- queued_at, name` was effectively alphabetical rather than topological.
ALTER TABLE pipeline_jobs
    ADD COLUMN job_key      TEXT    NOT NULL DEFAULT '',
    ADD COLUMN matrix       JSONB   NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN fail_fast    BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN max_parallel INT     NOT NULL DEFAULT 0 CHECK (max_parallel >= 0),
    ADD COLUMN ordinal      INT     NOT NULL DEFAULT 0 CHECK (ordinal >= 0);

-- Pre-matrix rows: the logical job id IS the name. This backfill is what makes
-- the `d.name = need` -> `d.job_key = need` switch safe for runs already in
-- flight at deploy time. It cannot leave an empty job_key behind — the parser's
-- job-id rule (isJobName) rejects an empty job id, so no existing row has an
-- empty name — which is why the CHECK below is safe to add immediately after.
UPDATE pipeline_jobs SET job_key = name WHERE job_key = '';

ALTER TABLE pipeline_jobs
    ALTER COLUMN job_key DROP DEFAULT,
    ADD CONSTRAINT pipeline_jobs_job_key_chk CHECK (job_key <> '');

-- Serves both the `needs` gate (every leg of job_key succeeded) and the
-- max-parallel sibling count in AcquireJob.
CREATE INDEX pipeline_jobs_group_idx ON pipeline_jobs (run_id, job_key, status);
