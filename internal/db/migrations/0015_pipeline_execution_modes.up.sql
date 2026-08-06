-- 0015_pipeline_execution_modes: placement contracts for pipeline jobs.
--
-- execution_mode is denormalized alongside runs_on/resource_tier because the
-- dispatcher must make placement decisions without parsing definition JSONB.
-- Isolated jobs are held until a reconciler reserves a concrete runner from
-- their requested pool; the reservation is deliberately separate from the
-- runner that eventually claims the job.

ALTER TABLE pipeline_jobs
    ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'shared',
    ADD COLUMN execution_pool TEXT NOT NULL DEFAULT '',
    ADD COLUMN reserved_runner_id UUID REFERENCES runners(id) ON DELETE SET NULL,
    ADD CONSTRAINT pipeline_jobs_execution_mode_chk
        CHECK (execution_mode IN ('shared', 'exclusive', 'isolated')),
    ADD CONSTRAINT pipeline_jobs_execution_pool_chk
        CHECK (
            (execution_mode = 'isolated' AND btrim(execution_pool) <> '')
            OR
            (execution_mode IN ('shared', 'exclusive') AND execution_pool = '')
        );

-- Lets the runner-row-locked dispatcher recompute actual concurrent work
-- without scanning completed job history.
CREATE INDEX pipeline_jobs_running_runner_idx
    ON pipeline_jobs (runner_id)
    WHERE status = 'running' AND runner_id IS NOT NULL;

-- Used by a future reconcile pass to discover unreserved isolated work by its
-- target pool, then by AcquireJob to find work reserved for a particular
-- runner. Generic queued-demand intentionally excludes these rows.
CREATE INDEX pipeline_jobs_isolated_unreserved_idx
    ON pipeline_jobs (org_id, execution_pool, queued_at)
    WHERE status = 'queued'
      AND execution_mode = 'isolated'
      AND reserved_runner_id IS NULL;
CREATE INDEX pipeline_jobs_isolated_reserved_idx
    ON pipeline_jobs (reserved_runner_id, queued_at)
    WHERE status = 'queued'
      AND execution_mode = 'isolated'
      AND reserved_runner_id IS NOT NULL;
