-- 0002_issues: Issues domain (CRUD, labels, comments, assignees).
--
-- Issue numbering is per-project monotonically increasing (like GitHub
-- "#123"). We allocate the next number with an UPSERT against
-- issue_number_seq inside the create transaction, so two concurrent inserts
-- will serialise on the row lock and never collide on the (project_id,
-- number) unique constraint.

-- ----------------------------------------------------------------------------
-- labels: project-scoped label dictionary, reused across issues.
--
-- Labels are scoped per project (not per org), mirroring how issues are
-- scoped: a label only makes sense in the context of one project's issue
-- tracker. Color is a 6-char hex string without leading "#" so it stays
-- compact in JSON; the HTTP layer accepts both forms.
-- ----------------------------------------------------------------------------
CREATE TABLE labels (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    color       TEXT NOT NULL DEFAULT '888888' CHECK (color ~ '^[0-9a-fA-F]{6}$'),
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX labels_project_name_lower_uk ON labels (project_id, LOWER(name));

-- ----------------------------------------------------------------------------
-- issue_number_seq: per-project counter that hands out the next issue
-- number. Locked row-by-row inside the issue create transaction so two
-- concurrent creates serialise instead of racing on the issues unique
-- index.
-- ----------------------------------------------------------------------------
CREATE TABLE issue_number_seq (
    project_id UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    next_value BIGINT NOT NULL DEFAULT 1 CHECK (next_value >= 1)
);

-- ----------------------------------------------------------------------------
-- issues: the work item itself.
--
-- author_id uses ON DELETE RESTRICT because losing the original author
-- mid-thread would silently rewrite history. If we ever need to delete a
-- user we'll surface the foreign key as a "transfer ownership" workflow
-- rather than dropping the rows. closed_by_id is intentionally weaker
-- (SET NULL) — it's only audit metadata and safe to forget.
-- ----------------------------------------------------------------------------
CREATE TABLE issues (
    id           UUID PRIMARY KEY,
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number       BIGINT NOT NULL CHECK (number >= 1),
    title        TEXT NOT NULL CHECK (LENGTH(title) >= 1),
    body         TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    author_id    UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    closed_at    TIMESTAMPTZ,
    closed_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issues_project_number_uk ON issues (project_id, number);
CREATE INDEX issues_project_state_idx        ON issues (project_id, state);
CREATE INDEX issues_author_idx                ON issues (author_id);
CREATE INDEX issues_project_created_idx       ON issues (project_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- issue_labels: M:N issues <-> labels.
-- ----------------------------------------------------------------------------
CREATE TABLE issue_labels (
    issue_id UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (issue_id, label_id)
);
CREATE INDEX issue_labels_label_idx ON issue_labels (label_id);

-- ----------------------------------------------------------------------------
-- issue_assignees: M:N issues <-> users.
--
-- ON DELETE CASCADE on user_id — if a user is removed entirely, dropping
-- their assignments is the right outcome. Authorship is preserved separately
-- via issues.author_id (RESTRICT).
-- ----------------------------------------------------------------------------
CREATE TABLE issue_assignees (
    issue_id    UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id)
);
CREATE INDEX issue_assignees_user_idx ON issue_assignees (user_id);

-- ----------------------------------------------------------------------------
-- issue_comments: top-level comments on an issue.
-- ----------------------------------------------------------------------------
CREATE TABLE issue_comments (
    id         UUID PRIMARY KEY,
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author_id  UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    body       TEXT NOT NULL CHECK (LENGTH(body) >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX issue_comments_issue_idx ON issue_comments (issue_id, created_at);
