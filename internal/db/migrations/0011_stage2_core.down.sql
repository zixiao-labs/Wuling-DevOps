DROP TABLE IF EXISTS project_releases;
DROP TABLE IF EXISTS artifact_package_versions;
DROP TABLE IF EXISTS artifact_packages;
DROP TABLE IF EXISTS test_runs;
DROP TABLE IF EXISTS test_cases;
DROP TABLE IF EXISTS test_suites;
DROP TABLE IF EXISTS test_plans;
DROP TABLE IF EXISTS work_items;
DROP TABLE IF EXISTS work_item_number_seq;
DROP TABLE IF EXISTS project_iterations;

ALTER TABLE repos
    DROP COLUMN IF EXISTS delete_branch_on_merge,
    DROP COLUMN IF EXISTS merge_strategies,
    DROP COLUMN IF EXISTS wiki_enabled,
    DROP COLUMN IF EXISTS issues_enabled,
    DROP COLUMN IF EXISTS topics;

ALTER TABLE projects
    DROP COLUMN IF EXISTS archived,
    DROP COLUMN IF EXISTS iteration_length_days,
    DROP COLUMN IF EXISTS work_item_prefix,
    DROP COLUMN IF EXISTS process_template;
