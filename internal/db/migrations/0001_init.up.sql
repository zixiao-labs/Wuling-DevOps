-- 0001_init: bootstrap schema for Stage 1.
--
-- We use UUIDs (v7-style time-ordered, generated in Go via google/uuid) as
-- primary keys for everything user-facing, and short slugs for URLs. Names
-- and slugs are unique within their parent scope, never globally.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ----------------------------------------------------------------------------
-- users: a real human (or a service account, distinguished by kind).
-- ----------------------------------------------------------------------------
CREATE TABLE users (
    id              UUID PRIMARY KEY,
    kind            TEXT NOT NULL DEFAULT 'human' CHECK (kind IN ('human', 'service')),
    username        TEXT NOT NULL,
    email           TEXT NOT NULL,
    display_name    TEXT NOT NULL DEFAULT '',
    -- argon2id PHC string (e.g. "$argon2id$v=19$m=...,t=...,p=...$salt$hash"); NULL for OAuth-only.
    password_hash   TEXT,
    -- GitHub OAuth identity once linked.
    github_user_id  BIGINT,
    github_login    TEXT,
    is_admin        BOOLEAN NOT NULL DEFAULT FALSE,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_lower_uk ON users (LOWER(username));
CREATE UNIQUE INDEX users_email_lower_uk    ON users (LOWER(email));
CREATE UNIQUE INDEX users_github_user_id_uk ON users (github_user_id) WHERE github_user_id IS NOT NULL;

-- ----------------------------------------------------------------------------
-- orgs: tenant container. A user always belongs to at least one org (their
-- personal org, auto-created on signup with slug = username).
-- ----------------------------------------------------------------------------
CREATE TABLE orgs (
    id           UUID PRIMARY KEY,
    slug         TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    is_personal  BOOLEAN NOT NULL DEFAULT FALSE,
    -- For personal orgs, the owning user. Null for team orgs.
    owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX orgs_slug_lower_uk ON orgs (LOWER(slug));

-- ----------------------------------------------------------------------------
-- org_members: user <-> org with a coarse role.
-- ----------------------------------------------------------------------------
CREATE TABLE org_members (
    org_id     UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX org_members_user_idx ON org_members (user_id);

-- ----------------------------------------------------------------------------
-- projects: containers under an org. Repos / Issues / MRs / Pipelines are
-- scoped here.
-- ----------------------------------------------------------------------------
CREATE TABLE projects (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    slug         TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    visibility   TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'internal', 'public')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX projects_org_slug_uk ON projects (org_id, LOWER(slug));

-- ----------------------------------------------------------------------------
-- repos: one row per Git repository. The bare repo on disk lives at
-- "<RepoRoot>/<org_id>/<project_id>/<id>.git". Storing IDs not slugs
-- in the path means renames don't move bytes.
-- ----------------------------------------------------------------------------
CREATE TABLE repos (
    id              UUID PRIMARY KEY,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL,
    display_name    TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    default_branch  TEXT NOT NULL DEFAULT 'main',
    visibility      TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'internal', 'public')),
    is_empty        BOOLEAN NOT NULL DEFAULT TRUE,
    size_bytes      BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX repos_project_slug_uk ON repos (project_id, LOWER(slug));

-- ----------------------------------------------------------------------------
-- access_tokens: long-lived tokens used by Git CLIs (HTTP basic auth) and
-- by future API clients that don't go through interactive login.
--
-- Plain JWTs would also work for git smart HTTP, but they expire too quickly
-- for typical CLI use. PATs are stored hashed (argon2id of the token bytes).
-- ----------------------------------------------------------------------------
CREATE TABLE access_tokens (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
    -- Coarse scopes; finer-grained perms will come in Stage 2.
    scopes      TEXT[] NOT NULL DEFAULT ARRAY['repo:read','repo:write']::TEXT[],
    last_used_at TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX access_tokens_user_idx ON access_tokens (user_id);
