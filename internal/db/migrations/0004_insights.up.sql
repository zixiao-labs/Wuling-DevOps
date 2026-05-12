-- 0004_insights: Insights commit index, populated on receive-pack exit so
-- the /insights/activity and /insights/contributors endpoints never have to
-- walk libgit2 on a UI request.
--
-- One row per (repo, commit OID). We INSERT ... ON CONFLICT DO NOTHING after
-- every push, so a re-push of an already-indexed commit is a cheap no-op.
-- Force-pushes leave the old commits in the index even after they're no
-- longer reachable; that's intentional — for the "activity in the last 30
-- days" view, removing a commit because a force-push hid it would surprise
-- users.

CREATE TABLE repo_commit_index (
    repo_id       UUID        NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    oid           CHAR(40)    NOT NULL CHECK (oid ~ '^[0-9a-f]{40}$'),
    author_name   TEXT        NOT NULL DEFAULT '',
    author_email  TEXT        NOT NULL DEFAULT '',
    author_time   TIMESTAMPTZ NOT NULL,
    indexed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, oid)
);

CREATE INDEX repo_commit_index_repo_time_idx
    ON repo_commit_index (repo_id, author_time DESC);

-- Lowered email for the contributors aggregation. Two commits authored by
-- "Alice <a@x>" and "alice <A@X>" should roll up together.
CREATE INDEX repo_commit_index_repo_email_idx
    ON repo_commit_index (repo_id, LOWER(author_email));
