-- 0003_merge_requests: Merge Requests domain (open/diff/merge, comments, reviews).
--
-- MR numbers are per-project monotonically increasing, allocated the same way
-- issue numbers are: an UPSERT against mr_number_seq inside the create
-- transaction serialises concurrent inserts on the row lock so two MRs never
-- collide on (project_id, number).
--
-- We deliberately do NOT share numbering with issues. GitHub does, but our URL
-- design splits issues and MRs into different paths (/issues/{n} vs
-- /merge-requests/{n}) so identical numbers across the two are unambiguous.
-- Keeping the counters separate avoids touching the live issuestore code.

-- ----------------------------------------------------------------------------
-- mr_number_seq: per-project counter that hands out the next MR number.
-- ----------------------------------------------------------------------------
CREATE TABLE mr_number_seq (
    project_id UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    next_value BIGINT NOT NULL DEFAULT 1 CHECK (next_value >= 1)
);

-- ----------------------------------------------------------------------------
-- merge_requests: source_ref into target_ref on a single repo.
--
-- source_oid_at_open / target_oid_at_open are recorded at create time for
-- audit and for detecting drift in the UI ("the source has been force-pushed
-- since this MR was opened"). The merge endpoint always re-resolves the live
-- tip of target_ref before merging — never trust the snapshot for the actual
-- ref-write.
--
-- author_id uses ON DELETE RESTRICT (same reasoning as issues: losing the
-- author silently rewrites history). merged_by_id / closed_by_id are audit
-- metadata and use SET NULL.
-- ----------------------------------------------------------------------------
CREATE TABLE merge_requests (
    id                  UUID PRIMARY KEY,
    repo_id             UUID NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    project_id          UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number              BIGINT NOT NULL CHECK (number >= 1),
    title               TEXT NOT NULL CHECK (LENGTH(title) >= 1),
    body                TEXT NOT NULL DEFAULT '',
    state               TEXT NOT NULL DEFAULT 'open'
                          CHECK (state IN ('open', 'merged', 'closed')),
    source_ref          TEXT NOT NULL,
    target_ref          TEXT NOT NULL,
    source_oid_at_open  CHAR(40) NOT NULL CHECK (source_oid_at_open ~ '^[0-9a-f]{40}$'),
    target_oid_at_open  CHAR(40) NOT NULL CHECK (target_oid_at_open ~ '^[0-9a-f]{40}$'),
    merge_strategy      TEXT
                          CHECK (merge_strategy IN ('ff', 'merge-commit', 'squash')),
    merge_commit_oid    CHAR(40) CHECK (merge_commit_oid IS NULL OR merge_commit_oid ~ '^[0-9a-f]{40}$'),
    merged_at           TIMESTAMPTZ,
    merged_by_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    closed_at           TIMESTAMPTZ,
    closed_by_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    author_id           UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A merged MR must carry strategy + merge OID + merged_at; an open one must not.
    CHECK (
        (state = 'merged' AND merge_strategy IS NOT NULL AND merge_commit_oid IS NOT NULL AND merged_at IS NOT NULL)
        OR
        (state <> 'merged' AND merge_strategy IS NULL AND merge_commit_oid IS NULL AND merged_at IS NULL)
    ),
    -- Source and target refs must differ; opening a self-merge is meaningless.
    CHECK (source_ref <> target_ref)
);
CREATE UNIQUE INDEX merge_requests_project_number_uk ON merge_requests (project_id, number);
CREATE INDEX merge_requests_repo_state_idx           ON merge_requests (repo_id, state);
CREATE INDEX merge_requests_target_ref_idx           ON merge_requests (repo_id, target_ref);
CREATE INDEX merge_requests_author_idx               ON merge_requests (author_id);
CREATE INDEX merge_requests_repo_created_idx         ON merge_requests (repo_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- mr_comments: discussion thread on an MR. Mirrors issue_comments shape so
-- the HTTP layer can keep the two response types parallel.
-- ----------------------------------------------------------------------------
CREATE TABLE mr_comments (
    id          UUID PRIMARY KEY,
    mr_id       UUID NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    body        TEXT NOT NULL CHECK (LENGTH(body) >= 1),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mr_comments_mr_idx ON mr_comments (mr_id, created_at);

-- ----------------------------------------------------------------------------
-- mr_reviews: an explicit review event with a state. Multiple reviews from
-- the same user are allowed (each is its own event); aggregation logic (which
-- one "wins" for branch-protection purposes) is Stage 2+.
-- ----------------------------------------------------------------------------
CREATE TABLE mr_reviews (
    id          UUID PRIMARY KEY,
    mr_id       UUID NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    state       TEXT NOT NULL
                  CHECK (state IN ('approved', 'changes_requested', 'commented')),
    body        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mr_reviews_mr_idx ON mr_reviews (mr_id, created_at);
