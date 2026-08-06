-- 0016_runner_isolation_self_checks: durable ownership for strong-isolation
-- VMs and the administrator-initiated, billable runner probe lifecycle.

-- An isolated VM is never generic pool capacity. Persisting its one target job
-- makes this invariant survive API/autoscaler restarts and gives cleanup a
-- durable provider-instance mapping.
ALTER TABLE runners
    ADD COLUMN isolated_job_id UUID REFERENCES pipeline_jobs(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX runners_isolated_job_id_uq
    ON runners (isolated_job_id)
    WHERE isolated_job_id IS NOT NULL;

-- One row represents one selected pool, not one multi-pool button click. That
-- lets Linux and Windows checks progress, fail, and clean up independently.
-- The probe secret is encrypted with the server secret box; its plaintext is
-- never returned through this table or an API response.
CREATE TABLE runner_self_checks (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    requested_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    pool_name TEXT NOT NULL,
    provider TEXT NOT NULL,
    os TEXT NOT NULL,

    phase TEXT NOT NULL DEFAULT 'preflight'
        CHECK (phase IN ('preflight', 'provision', 'wait_runner', 'execute', 'cleanup')),
    state TEXT NOT NULL DEFAULT 'preflight'
        CHECK (state IN (
            'preflight', 'queued', 'provisioning', 'waiting_for_runner',
            'executing', 'cleanup_pending', 'succeeded', 'failed', 'cleaned'
        )),
    checks JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary TEXT NOT NULL DEFAULT '',

    run_id UUID REFERENCES pipeline_runs(id) ON DELETE SET NULL,
    job_id UUID REFERENCES pipeline_jobs(id) ON DELETE SET NULL,
    runner_id UUID REFERENCES runners(id) ON DELETE SET NULL,
    external_id TEXT NOT NULL DEFAULT '',

    secret_ciphertext BYTEA,
    secret_nonce BYTEA,
    secret_hash BYTEA NOT NULL,

    cleanup_attempts INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_attempts >= 0),
    cleanup_last_error TEXT NOT NULL DEFAULT '',
    next_cleanup_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    cleaned_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX runner_self_checks_org_created_idx
    ON runner_self_checks (org_id, created_at DESC);
CREATE INDEX runner_self_checks_cleanup_idx
    ON runner_self_checks (next_cleanup_at)
    WHERE state = 'cleanup_pending';
CREATE UNIQUE INDEX runner_self_checks_job_uq
    ON runner_self_checks (job_id)
    WHERE job_id IS NOT NULL;
-- A double-click or retried admin request must not turn into a fleet of
-- billable probe VMs for the same pool. A record stops blocking a new probe
-- only after all temporary resources have been cleaned (or no resource was
-- created at all).
CREATE UNIQUE INDEX runner_self_checks_active_pool_uq
    ON runner_self_checks (org_id, pool_name)
    WHERE cleaned_at IS NULL;
