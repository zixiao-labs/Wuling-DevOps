-- 0009_pipelines: Pipelines (CI/CD) + Secrets + Runners (Stage 1).
--
-- See docs/pipelines.md for the full design. Conventions mirror earlier
-- migrations: UUID PKs generated in Go, per-repo monotonic numbering via an
-- UPSERT-locked *_number_seq table (same pattern as issue_number_seq), and
-- *_at timestamps maintained in application code (no triggers).
--
-- Everything here is ORG-SCOPED. There is no global runner pool, no global
-- secret, no global autoscaler config — that is a hard requirement.

-- ----------------------------------------------------------------------------
-- secrets: org- or project-scoped encrypted values (AES-256-GCM). The
-- ciphertext + nonce are stored; the plaintext is decrypted only when handed
-- to a runner (job acquire) or used as a cloud credential by the autoscaler.
-- The name is constrained to an env-var-safe shape so it can be injected as
-- an environment variable verbatim.
-- ----------------------------------------------------------------------------
CREATE TABLE secrets (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    project_id  UUID REFERENCES projects(id) ON DELETE CASCADE,
    scope       TEXT NOT NULL CHECK (scope IN ('org', 'project')),
    name        TEXT NOT NULL CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]*$'),
    ciphertext  BYTEA NOT NULL,
    nonce       BYTEA NOT NULL,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- scope and project_id must agree: org secrets have no project, project
    -- secrets must name one.
    CONSTRAINT secrets_scope_project_chk CHECK (
        (scope = 'org'     AND project_id IS NULL) OR
        (scope = 'project' AND project_id IS NOT NULL)
    )
);
-- One name per scope. Partial unique indexes key on whichever owner applies;
-- an org and one of its projects may both define "NPM_TOKEN" (project wins at
-- resolution time).
CREATE UNIQUE INDEX secrets_org_name_uk
    ON secrets (org_id, name) WHERE project_id IS NULL;
CREATE UNIQUE INDEX secrets_project_name_uk
    ON secrets (project_id, name) WHERE project_id IS NOT NULL;

-- ----------------------------------------------------------------------------
-- runners: an org-scoped execution agent. `static` runners are registered by
-- hand; the others are ephemeral VMs the autoscaler launched on a cloud /
-- hypervisor and will release once idle past runner-config.yaml's
-- idle_timeout.
--
-- The token (wlrt_…) embeds this row's id so the resolver can load the row in
-- O(1) and then argon2id-verify the secret half (same hashing as PATs).
--
-- last_job_at drives idle scale-down: the autoscaler measures idleness from
-- the moment the runner's most recent job finished, NOT from boot — frequently
-- starting/stopping pay-as-you-go instances is slow and expensive.
-- ----------------------------------------------------------------------------
CREATE TABLE runners (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    token_hash    TEXT NOT NULL,
    labels        TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    resource_tier TEXT NOT NULL DEFAULT 'medium' CHECK (resource_tier IN ('low', 'medium', 'high')),
    provider      TEXT NOT NULL DEFAULT 'static'
                  CHECK (provider IN ('static', 'aliyun', 'aws', 'proxmox', 'vcenter')),
    pool_name     TEXT NOT NULL DEFAULT '',
    ephemeral     BOOLEAN NOT NULL DEFAULT FALSE,
    external_id   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'offline'
                  CHECK (status IN ('offline', 'idle', 'busy')),
    last_seen_at  TIMESTAMPTZ,
    last_job_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX runners_org_name_uk ON runners (org_id, LOWER(name));
CREATE INDEX runners_org_idx  ON runners (org_id);
CREATE INDEX runners_pool_idx ON runners (org_id, pool_name) WHERE ephemeral;

-- ----------------------------------------------------------------------------
-- runner_registration_tokens: short-lived, single-use tokens that a runner
-- client redeems (POST /runner/register) to obtain a persistent runner token.
-- An org maintainer mints one via the UI for static runners; the autoscaler
-- mints one per launched VM and injects it via cloud-init / user-data. The
-- hint columns are copied onto the runner row created at redemption.
-- ----------------------------------------------------------------------------
CREATE TABLE runner_registration_tokens (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    token_hash    TEXT NOT NULL,
    labels        TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    resource_tier TEXT NOT NULL DEFAULT 'medium' CHECK (resource_tier IN ('low', 'medium', 'high')),
    provider      TEXT NOT NULL DEFAULT 'static'
                  CHECK (provider IN ('static', 'aliyun', 'aws', 'proxmox', 'vcenter')),
    pool_name     TEXT NOT NULL DEFAULT '',
    ephemeral     BOOLEAN NOT NULL DEFAULT FALSE,
    external_id   TEXT NOT NULL DEFAULT '',
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX runner_reg_tokens_org_idx     ON runner_registration_tokens (org_id);
CREATE INDEX runner_reg_tokens_expires_idx ON runner_registration_tokens (expires_at)
    WHERE used_at IS NULL;

-- ----------------------------------------------------------------------------
-- pipeline_runs: one execution of one workflow file for a commit/event.
-- `definition` is the parsed-at-trigger snapshot so a re-run is reproducible
-- even if the file later changes on the branch.
-- ----------------------------------------------------------------------------
CREATE TABLE pipeline_runs (
    id             UUID PRIMARY KEY,
    org_id         UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    project_id     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repo_id        UUID NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    number         BIGINT NOT NULL CHECK (number >= 1),
    workflow_path  TEXT NOT NULL,
    workflow_name  TEXT NOT NULL DEFAULT '',
    event          TEXT NOT NULL CHECK (event IN ('push', 'pull_request', 'manual')),
    git_ref        TEXT NOT NULL DEFAULT '',
    commit_sha     TEXT NOT NULL,
    commit_message TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued', 'running', 'success', 'failed', 'canceled')),
    triggered_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    definition     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX pipeline_runs_repo_number_uk ON pipeline_runs (repo_id, number);
CREATE INDEX pipeline_runs_project_idx ON pipeline_runs (project_id, created_at DESC);
CREATE INDEX pipeline_runs_repo_idx    ON pipeline_runs (repo_id, created_at DESC);
CREATE INDEX pipeline_runs_status_idx  ON pipeline_runs (status);

-- per-repo run numbering (#1, #2, …); same locked-UPSERT pattern as issues.
CREATE TABLE pipeline_run_number_seq (
    repo_id    UUID PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    next_value BIGINT NOT NULL DEFAULT 1 CHECK (next_value >= 1)
);

-- ----------------------------------------------------------------------------
-- pipeline_jobs: a job within a run. org_id is denormalized so the runner
-- acquire query (hot path, runs on every long-poll) can filter by org +
-- status + labels without joining up to the run/project. resource_tier and
-- runs_on drive matching against runner labels/tier.
-- ----------------------------------------------------------------------------
CREATE TABLE pipeline_jobs (
    id            UUID PRIMARY KEY,
    run_id        UUID NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    runs_on       TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    resource_tier TEXT NOT NULL DEFAULT 'medium' CHECK (resource_tier IN ('low', 'medium', 'high')),
    needs         TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    status        TEXT NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued', 'running', 'success', 'failed', 'canceled')),
    runner_id     UUID REFERENCES runners(id) ON DELETE SET NULL,
    definition    JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempt       INT NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    log_size      BIGINT NOT NULL DEFAULT 0,
    queued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    UNIQUE (run_id, name)
);
CREATE INDEX pipeline_jobs_run_idx      ON pipeline_jobs (run_id);
CREATE INDEX pipeline_jobs_dispatch_idx ON pipeline_jobs (org_id, queued_at) WHERE status = 'queued';
CREATE INDEX pipeline_jobs_runner_idx   ON pipeline_jobs (runner_id) WHERE runner_id IS NOT NULL;

-- ----------------------------------------------------------------------------
-- pipeline_steps: ordered steps within a job. Logs are stored on disk
-- (WULING_PIPELINE_LOG_DIR), not here — this table tracks status/timing only.
-- ----------------------------------------------------------------------------
CREATE TABLE pipeline_steps (
    id          UUID PRIMARY KEY,
    job_id      UUID NOT NULL REFERENCES pipeline_jobs(id) ON DELETE CASCADE,
    number      INT NOT NULL CHECK (number >= 1),
    name        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued', 'running', 'success', 'failed', 'canceled', 'skipped')),
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (job_id, number)
);
CREATE INDEX pipeline_steps_job_idx ON pipeline_steps (job_id, number);
