-- 0010_runner_os: add the OS dimension to runners and their registration tokens.
--
-- The OS drives the runner client's execution backend (Linux/Windows containers
-- vs a host shell) and the autoscaler's bootstrap (cloud-init vs PowerShell).
-- It defaults to 'linux' so every existing runner row and outstanding
-- registration token keeps its current Stage-1 behavior. macOS runners are
-- manual-registration only; the autoscaler rejects macos pools (see
-- internal/autoscale). Job→runner routing still rides on the labels[] match in
-- AcquireJob (the runner advertises an `os:<x>` label), so this column is for
-- bootstrap selection, validation, and UI display — not the dispatch hot path.
ALTER TABLE runners
    ADD COLUMN os TEXT NOT NULL DEFAULT 'linux'
        CHECK (os IN ('linux', 'windows', 'macos'));

ALTER TABLE runner_registration_tokens
    ADD COLUMN os TEXT NOT NULL DEFAULT 'linux'
        CHECK (os IN ('linux', 'windows', 'macos'));
