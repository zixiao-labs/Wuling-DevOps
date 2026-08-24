-- Track the Wuling-owned GitHub Check Run that mirrors each observed Actions
-- workflow or third-party check. Webhook retries can PATCH the existing run
-- instead of creating duplicate branch-protection contexts.
ALTER TABLE github_check_states
    ADD COLUMN feedback_check_run_id BIGINT;

CREATE UNIQUE INDEX github_check_states_feedback_run_uk
    ON github_check_states (feedback_check_run_id)
    WHERE feedback_check_run_id IS NOT NULL;
