-- 0006_user_approval: registration approval workflow.
--
-- Every newly created user starts in `pending` state and cannot log in until
-- an admin promotes them to `approved`. Existing rows at migration time are
-- back-filled to `approved` so we don't lock anybody out on upgrade.
--
-- We also widen the GitHub OAuth columns introduced in 0001 with the audit
-- fields we need for the linking flow.

ALTER TABLE users
    ADD COLUMN approval_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (approval_status IN ('pending', 'approved', 'rejected')),
    ADD COLUMN approval_note   TEXT NOT NULL DEFAULT '',
    ADD COLUMN approved_at     TIMESTAMPTZ,
    ADD COLUMN approved_by     UUID REFERENCES users(id) ON DELETE SET NULL;

-- Anyone who already had an account at the time of the upgrade is grandfathered
-- in: they signed up before this gate existed, so we don't make them wait.
UPDATE users
   SET approval_status = 'approved',
       approved_at     = now()
 WHERE approval_status = 'pending';

CREATE INDEX users_approval_status_idx
    ON users (approval_status)
    WHERE approval_status <> 'approved';
