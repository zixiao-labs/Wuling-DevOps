-- Maps a GitHub App installation repository to a Wuling bare repo.
-- Webhook handlers skip events whose (owner, name) is not linked.
CREATE TABLE github_repo_links (
    id               UUID PRIMARY KEY,
    installation_id  BIGINT      NOT NULL,
    owner            TEXT        NOT NULL,
    name             TEXT        NOT NULL,
    org_id           UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    project_id       UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repo_id          UUID        NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    active           BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT github_repo_links_owner_name_chk CHECK (owner <> '' AND name <> ''),
    CONSTRAINT github_repo_links_repo_uk UNIQUE (repo_id)
);

CREATE UNIQUE INDEX github_repo_links_full_name_uk
    ON github_repo_links (LOWER(owner), LOWER(name))
    WHERE active;

CREATE INDEX github_repo_links_installation_idx
    ON github_repo_links (installation_id)
    WHERE active;
