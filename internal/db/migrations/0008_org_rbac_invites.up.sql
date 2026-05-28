-- 0008_org_rbac_invites: org-level RBAC widening + invitations + avatars.
--
-- This migration touches three loosely related but co-shipped surfaces:
--
-- 1. users.avatar_updated_at — null when the user has never uploaded a
--    custom avatar (we fall back to a deterministic initials tile in the
--    frontend). When non-null it doubles as a cache-buster for the public
--    /users/{username}/avatar endpoint.
--
-- 2. org_members.role widens from {owner,admin,member} to a GitLab-style
--    five-tier scheme {owner,maintainer,developer,reporter,guest}. The old
--    names map cleanly onto the new ones: 'admin' is a maintainer (could
--    create projects but not delete the org), and 'member' is a developer
--    (could push to repos). Reporter and guest are new and only assigned
--    via the new invitation/role-change endpoints.
--
-- 3. org_invitations holds magic-link invitations. invitee_user_id is set
--    when the inviter typed a known username; invitee_email is set when
--    they typed an email (whether or not it matches an existing account
--    yet). At least one must be non-null. token_hash is HMAC-SHA256 of the
--    raw token (same pattern as oauth_access_tokens.token_hash) so a DB
--    dump can't be replayed to join the org.

-- ----------------------------------------------------------------------------
-- 1. avatar metadata on users
-- ----------------------------------------------------------------------------
ALTER TABLE users
    ADD COLUMN avatar_updated_at TIMESTAMPTZ;

-- ----------------------------------------------------------------------------
-- 2. expand org_members.role
--
-- Drop the existing three-value CHECK before remapping rows — otherwise the
-- UPDATE writes 'maintainer'/'developer' values that violate the still-active
-- constraint. After the data is migrated we re-add the constraint with the
-- five-value enumeration.
-- ----------------------------------------------------------------------------
ALTER TABLE org_members
    DROP CONSTRAINT org_members_role_check;

UPDATE org_members SET role = 'maintainer' WHERE role = 'admin';
UPDATE org_members SET role = 'developer'  WHERE role = 'member';

ALTER TABLE org_members
    ADD CONSTRAINT org_members_role_check
        CHECK (role IN ('owner', 'maintainer', 'developer', 'reporter', 'guest'));

-- ----------------------------------------------------------------------------
-- 3. invitations
--
-- status walks pending -> accepted | revoked | expired. We do NOT garbage-
-- collect expired rows here; a periodic sweep can run later if the table
-- grows. accepted_at / accepted_by are filled at accept-time so the audit
-- trail survives the join.
-- ----------------------------------------------------------------------------
CREATE TABLE org_invitations (
    id               UUID PRIMARY KEY,
    org_id           UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    inviter_user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invitee_user_id  UUID REFERENCES users(id) ON DELETE CASCADE,
    invitee_email    TEXT,
    role             TEXT NOT NULL
                     CHECK (role IN ('maintainer', 'developer', 'reporter', 'guest')),
    token_hash       TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at      TIMESTAMPTZ,
    accepted_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    -- At least one of (invitee_user_id, invitee_email) must point somewhere.
    CONSTRAINT org_invitations_invitee_present
        CHECK (invitee_user_id IS NOT NULL OR invitee_email IS NOT NULL)
);

-- One pending invitation per (org, invitee). Two NULLs collate as distinct in
-- a regular unique index, so we use partial indexes keyed on whichever
-- invitee identifier is set. This lets a user be invited by username AND by
-- email simultaneously (unlikely, but not worth forbidding) while preventing
-- two simultaneous pending invitations on the same key.
CREATE UNIQUE INDEX org_invitations_pending_user_uk
    ON org_invitations (org_id, invitee_user_id)
    WHERE status = 'pending' AND invitee_user_id IS NOT NULL;
CREATE UNIQUE INDEX org_invitations_pending_email_uk
    ON org_invitations (org_id, LOWER(invitee_email))
    WHERE status = 'pending' AND invitee_email IS NOT NULL;
CREATE UNIQUE INDEX org_invitations_token_hash_uk
    ON org_invitations (token_hash);
CREATE INDEX org_invitations_org_idx     ON org_invitations (org_id);
CREATE INDEX org_invitations_expires_idx ON org_invitations (expires_at)
    WHERE status = 'pending';
