DROP INDEX IF EXISTS github_check_states_feedback_run_uk;

ALTER TABLE github_check_states
    DROP COLUMN IF EXISTS feedback_check_run_id;
