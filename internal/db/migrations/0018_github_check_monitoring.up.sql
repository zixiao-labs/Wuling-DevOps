-- Latest terminal state observed from GitHub Actions workflows and Checks.
-- The control plane only observes external checks; it never mutates a check
-- run owned by GitHub Actions or another GitHub App.
CREATE TABLE github_check_states (
    id             UUID PRIMARY KEY,
    repo_id        UUID        NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    source         TEXT        NOT NULL,
    external_id    TEXT        NOT NULL,
    name           TEXT        NOT NULL,
    provider       TEXT        NOT NULL DEFAULT '',
    head_sha       TEXT        NOT NULL DEFAULT '',
    status         TEXT        NOT NULL DEFAULT 'completed',
    conclusion     TEXT        NOT NULL,
    color          TEXT        NOT NULL,
    attempt        INTEGER     NOT NULL DEFAULT 1,
    details_url    TEXT        NOT NULL DEFAULT '',
    completed_at   TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT github_check_states_source_chk
        CHECK (source IN ('check_run', 'workflow_run')),
    CONSTRAINT github_check_states_status_chk
        CHECK (status = 'completed'),
    CONSTRAINT github_check_states_color_chk
        CHECK (color IN ('green', 'red')),
    CONSTRAINT github_check_states_attempt_chk
        CHECK (attempt > 0),
    CONSTRAINT github_check_states_external_uk
        UNIQUE (repo_id, source, external_id)
);

CREATE INDEX github_check_states_repo_completed_idx
    ON github_check_states (repo_id, completed_at DESC);

-- Durable hand-off point for the future Wuling notification dispatcher.
-- Producers insert idempotent domain events now; a later worker can claim
-- rows with delivered_at IS NULL and fan them out to inbox/push/email/etc.
CREATE TABLE notification_outbox (
    id             UUID PRIMARY KEY,
    dedupe_key     TEXT        NOT NULL UNIQUE,
    event_type     TEXT        NOT NULL,
    org_id         UUID        REFERENCES orgs(id) ON DELETE CASCADE,
    repo_id        UUID        REFERENCES repos(id) ON DELETE CASCADE,
    payload        JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    available_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at   TIMESTAMPTZ,
    attempts       INTEGER     NOT NULL DEFAULT 0,
    last_error     TEXT        NOT NULL DEFAULT '',
    CONSTRAINT notification_outbox_event_type_chk CHECK (event_type <> ''),
    CONSTRAINT notification_outbox_attempts_chk CHECK (attempts >= 0)
);

CREATE INDEX notification_outbox_pending_idx
    ON notification_outbox (available_at, created_at)
    WHERE delivered_at IS NULL;
