-- Reverse of 0009_pipelines. Drop in FK-dependency order (children first).
DROP TABLE IF EXISTS pipeline_steps;
DROP TABLE IF EXISTS pipeline_jobs;
DROP TABLE IF EXISTS pipeline_run_number_seq;
DROP TABLE IF EXISTS pipeline_runs;
DROP TABLE IF EXISTS runner_registration_tokens;
DROP TABLE IF EXISTS runners;
DROP TABLE IF EXISTS secrets;
