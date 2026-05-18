-- 0006_user_approval (down).

DROP INDEX IF EXISTS users_approval_status_idx;

ALTER TABLE users
    DROP COLUMN IF EXISTS approval_status,
    DROP COLUMN IF EXISTS approval_note,
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS approved_by;
