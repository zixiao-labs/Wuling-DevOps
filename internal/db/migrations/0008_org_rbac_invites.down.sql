-- 0008_org_rbac_invites (down).

DROP TABLE IF EXISTS org_invitations;

-- Drop the five-value CHECK before collapsing rows back to the original
-- enumeration — the remap writes 'admin'/'member' values that would otherwise
-- violate the still-active constraint. Re-add the three-value constraint
-- afterwards.
ALTER TABLE org_members
    DROP CONSTRAINT IF EXISTS org_members_role_check;

UPDATE org_members SET role = 'admin'  WHERE role IN ('maintainer');
UPDATE org_members SET role = 'member' WHERE role IN ('developer', 'reporter', 'guest');

ALTER TABLE org_members
    ADD CONSTRAINT org_members_role_check
        CHECK (role IN ('owner', 'admin', 'member'));

ALTER TABLE users
    DROP COLUMN IF EXISTS avatar_updated_at;
