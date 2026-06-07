-- Reverse of 0010_runner_os.
ALTER TABLE runner_registration_tokens DROP COLUMN IF EXISTS os;
ALTER TABLE runners DROP COLUMN IF EXISTS os;
