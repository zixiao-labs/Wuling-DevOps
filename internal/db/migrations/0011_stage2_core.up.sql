-- 0011_stage2_core: project planning, test management, repository policy,
-- and artifact catalogue foundations for Stage 2.
--
-- Binary artifact bytes deliberately do not live in PostgreSQL. Version rows
-- carry an immutable blob_key that the standalone artifact service resolves
-- through its configured storage provider (disk / S3 / R2 / OSS).

ALTER TABLE projects
    ADD COLUMN process_template TEXT NOT NULL DEFAULT 'scrum'
        CHECK (process_template IN ('scrum', 'kanban', 'basic')),
    ADD COLUMN work_item_prefix TEXT NOT NULL DEFAULT '',
    ADD COLUMN iteration_length_days SMALLINT NOT NULL DEFAULT 14
        CHECK (iteration_length_days BETWEEN 1 AND 90),
    ADD COLUMN archived BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE repos
    ADD COLUMN topics TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    ADD COLUMN issues_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN wiki_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN merge_strategies TEXT[] NOT NULL DEFAULT ARRAY['merge','squash','rebase']::TEXT[],
    ADD COLUMN delete_branch_on_merge BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE project_iterations (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    goal        TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'planned'
                CHECK (state IN ('planned', 'current', 'closed')),
    starts_at   DATE NOT NULL,
    ends_at     DATE NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT project_iterations_dates_chk CHECK (ends_at >= starts_at),
    UNIQUE (project_id, name),
    UNIQUE (project_id, id)
);
CREATE INDEX project_iterations_project_dates_idx
    ON project_iterations (project_id, starts_at, ends_at);
CREATE UNIQUE INDEX project_iterations_one_current_uk
    ON project_iterations (project_id) WHERE state = 'current';

CREATE TABLE work_item_number_seq (
    project_id  UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    next_number BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE work_items (
    id             UUID PRIMARY KEY,
    project_id     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number         BIGINT NOT NULL,
    parent_id      UUID,
    iteration_id   UUID,
    assignee_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    author_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    type           TEXT NOT NULL DEFAULT 'task'
                   CHECK (type IN ('epic', 'feature', 'user_story', 'task', 'bug')),
    title          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'new'
                   CHECK (state IN ('new', 'active', 'resolved', 'closed')),
    priority       SMALLINT NOT NULL DEFAULT 2 CHECK (priority BETWEEN 0 AND 4),
    story_points   NUMERIC(7,2) CHECK (story_points IS NULL OR story_points >= 0),
    area_path      TEXT NOT NULL DEFAULT '',
    backlog_order  DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at      TIMESTAMPTZ,
    UNIQUE (project_id, number),
    UNIQUE (project_id, id),
    FOREIGN KEY (project_id, parent_id)
        REFERENCES work_items(project_id, id),
    FOREIGN KEY (project_id, iteration_id)
        REFERENCES project_iterations(project_id, id)
);
CREATE INDEX work_items_project_backlog_idx
    ON work_items (project_id, state, backlog_order, number);
CREATE INDEX work_items_iteration_idx ON work_items (iteration_id, state);

CREATE TABLE test_plans (
    id           UUID PRIMARY KEY,
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    iteration_id UUID,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL DEFAULT 'active'
                 CHECK (state IN ('active', 'completed', 'archived')),
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id, iteration_id)
        REFERENCES project_iterations(project_id, id)
);

CREATE TABLE test_suites (
    id          UUID PRIMARY KEY,
    plan_id     UUID NOT NULL REFERENCES test_plans(id) ON DELETE CASCADE,
    parent_id   UUID,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (plan_id, parent_id, name),
    UNIQUE (plan_id, id),
    FOREIGN KEY (plan_id, parent_id)
        REFERENCES test_suites(plan_id, id) ON DELETE CASCADE
);

CREATE TABLE test_cases (
    id             UUID PRIMARY KEY,
    suite_id       UUID NOT NULL REFERENCES test_suites(id) ON DELETE CASCADE,
    title          TEXT NOT NULL,
    steps          JSONB NOT NULL DEFAULT '[]'::JSONB,
    expected       TEXT NOT NULL DEFAULT '',
    automation     TEXT NOT NULL DEFAULT 'manual'
                   CHECK (automation IN ('manual', 'lightning')),
    automation_ref TEXT NOT NULL DEFAULT '',
    priority       SMALLINT NOT NULL DEFAULT 2 CHECK (priority BETWEEN 0 AND 4),
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE test_runs (
    id           UUID PRIMARY KEY,
    test_case_id UUID NOT NULL REFERENCES test_cases(id) ON DELETE CASCADE,
    status       TEXT NOT NULL CHECK (status IN ('passed', 'failed', 'blocked', 'skipped')),
    duration_ms  BIGINT CHECK (duration_ms IS NULL OR duration_ms >= 0),
    notes        TEXT NOT NULL DEFAULT '',
    run_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    run_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX test_runs_case_time_idx ON test_runs (test_case_id, run_at DESC);

CREATE TABLE artifact_packages (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('npm', 'pypi', 'cargo', 'docker', 'logos')),
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, kind, name)
);

CREATE TABLE artifact_package_versions (
    id           UUID PRIMARY KEY,
    package_id   UUID NOT NULL REFERENCES artifact_packages(id) ON DELETE CASCADE,
    version      TEXT NOT NULL,
    blob_key     TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    sha256       TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    metadata     JSONB NOT NULL DEFAULT '{}'::JSONB,
    published_by UUID REFERENCES users(id) ON DELETE SET NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (package_id, version)
);

CREATE TABLE project_releases (
    id           UUID PRIMARY KEY,
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tag_name     TEXT NOT NULL,
    name         TEXT NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    prerelease   BOOLEAN NOT NULL DEFAULT FALSE,
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    UNIQUE (project_id, tag_name)
);
