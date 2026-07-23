package stage2store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// --- test plans -----------------------------------------------------------------

type CreateTestPlanParams struct {
	ProjectID   uuid.UUID
	IterationID *uuid.UUID
	Name        string
	Description string
	CreatedBy   uuid.UUID
}

func (s *Store) CreateTestPlan(ctx context.Context, p CreateTestPlanParams) (*model.TestPlan, error) {
	out := &model.TestPlan{ID: uuid.New(), ProjectID: p.ProjectID,
		IterationID: p.IterationID, Name: strings.TrimSpace(p.Name),
		Description: p.Description, State: "active", CreatedBy: &p.CreatedBy}
	err := s.pool.QueryRow(ctx, `INSERT INTO test_plans
		(id, project_id, iteration_id, name, description, state, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at, updated_at`,
		out.ID, out.ProjectID, out.IterationID, out.Name, out.Description,
		out.State, out.CreatedBy).Scan(&out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, mapDBError(err, "test plan")
	}
	return out, nil
}

func (s *Store) ListTestPlans(ctx context.Context, projectID uuid.UUID) ([]model.TestPlan, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, project_id, iteration_id, name,
		description, state, created_by, created_at, updated_at
		FROM test_plans WHERE project_id = $1 ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.TestPlan, 0)
	for rows.Next() {
		var item model.TestPlan
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.IterationID, &item.Name,
			&item.Description, &item.State, &item.CreatedBy, &item.CreatedAt,
			&item.UpdatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err(), "test plans")
}

type CreateTestSuiteParams struct {
	ProjectID   uuid.UUID
	PlanID      uuid.UUID
	ParentID    *uuid.UUID
	Name        string
	Description string
}

func (s *Store) CreateTestSuite(ctx context.Context, p CreateTestSuiteParams) (*model.TestSuite, error) {
	out := &model.TestSuite{ID: uuid.New(), PlanID: p.PlanID, ParentID: p.ParentID,
		Name: strings.TrimSpace(p.Name), Description: p.Description}
	err := s.pool.QueryRow(ctx, `INSERT INTO test_suites
		(id, plan_id, parent_id, name, description)
		SELECT $1,$2,$3,$4,$5 WHERE EXISTS (
			SELECT 1 FROM test_plans WHERE id = $2 AND project_id = $6
		) RETURNING created_at`, out.ID, out.PlanID, out.ParentID, out.Name,
		out.Description, p.ProjectID).Scan(&out.CreatedAt)
	if err != nil {
		return nil, mapDBError(err, "test suite")
	}
	return out, nil
}

func (s *Store) ListTestSuites(ctx context.Context, projectID, planID uuid.UUID) ([]model.TestSuite, error) {
	rows, err := s.pool.Query(ctx, `SELECT ts.id, ts.plan_id, ts.parent_id, ts.name,
		ts.description, ts.created_at FROM test_suites ts
		JOIN test_plans tp ON tp.id = ts.plan_id
		WHERE tp.project_id = $1 AND ts.plan_id = $2 ORDER BY ts.created_at, ts.name`, projectID, planID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.TestSuite, 0)
	for rows.Next() {
		var item model.TestSuite
		if err := rows.Scan(&item.ID, &item.PlanID, &item.ParentID, &item.Name,
			&item.Description, &item.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err(), "test suites")
}

type CreateTestCaseParams struct {
	SuiteID       uuid.UUID
	Title         string
	Steps         json.RawMessage
	Expected      string
	Automation    string
	AutomationRef string
	Priority      *int
	CreatedBy     uuid.UUID
}

func (s *Store) CreateTestCase(ctx context.Context, projectID uuid.UUID, p CreateTestCaseParams) (*model.TestCase, error) {
	if len(p.Steps) == 0 {
		p.Steps = json.RawMessage("[]")
	}
	if !json.Valid(p.Steps) {
		return nil, apperr.Validation("test steps must be valid JSON", nil)
	}
	out := &model.TestCase{ID: uuid.New(), SuiteID: p.SuiteID,
		Title: strings.TrimSpace(p.Title), Steps: p.Steps, Expected: p.Expected,
		Automation: p.Automation, AutomationRef: p.AutomationRef,
		Priority: 2, CreatedBy: &p.CreatedBy}
	if out.Automation == "" {
		out.Automation = "manual"
	}
	if p.Priority != nil {
		out.Priority = *p.Priority
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO test_cases
		(id, suite_id, title, steps, expected, automation, automation_ref, priority, created_by)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9
		WHERE EXISTS (
			SELECT 1 FROM test_suites ts JOIN test_plans tp ON tp.id = ts.plan_id
			WHERE ts.id = $2 AND tp.project_id = $10
		) RETURNING created_at, updated_at`, out.ID, out.SuiteID, out.Title,
		out.Steps, out.Expected, out.Automation, out.AutomationRef, out.Priority,
		out.CreatedBy, projectID).Scan(&out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, mapDBError(err, "test case")
	}
	return out, nil
}

func (s *Store) ListTestCases(ctx context.Context, projectID, suiteID uuid.UUID) ([]model.TestCase, error) {
	rows, err := s.pool.Query(ctx, `SELECT tc.id, tc.suite_id, tc.title, tc.steps,
		tc.expected, tc.automation, tc.automation_ref, tc.priority, tc.created_by,
		tc.created_at, tc.updated_at, tr.id, tr.status, tr.duration_ms, tr.notes,
		tr.run_by, tr.run_at
		FROM test_cases tc
		JOIN test_suites ts ON ts.id = tc.suite_id
		JOIN test_plans tp ON tp.id = ts.plan_id
		LEFT JOIN LATERAL (
			SELECT * FROM test_runs WHERE test_case_id = tc.id ORDER BY run_at DESC LIMIT 1
		) tr ON true
		WHERE tp.project_id = $1 AND tc.suite_id = $2
		ORDER BY tc.created_at, tc.title`, projectID, suiteID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.TestCase, 0)
	for rows.Next() {
		var item model.TestCase
		var runID *uuid.UUID
		var status, notes *string
		var duration *int64
		var runBy *uuid.UUID
		var runAt *time.Time
		if err := rows.Scan(&item.ID, &item.SuiteID, &item.Title, &item.Steps,
			&item.Expected, &item.Automation, &item.AutomationRef, &item.Priority,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt, &runID, &status,
			&duration, &notes, &runBy, &runAt); err != nil {
			return nil, apperr.Internal(err)
		}
		if runID != nil {
			item.LastRun = &model.TestRun{ID: *runID, TestCaseID: item.ID,
				Status: *status, DurationMS: duration, Notes: *notes, RunBy: runBy,
				RunAt: *runAt}
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err(), "test cases")
}

type RecordTestRunParams struct {
	TestCaseID uuid.UUID
	Status     string
	DurationMS *int64
	Notes      string
	RunBy      uuid.UUID
}

func (s *Store) RecordTestRun(ctx context.Context, projectID uuid.UUID, p RecordTestRunParams) (*model.TestRun, error) {
	out := &model.TestRun{ID: uuid.New(), TestCaseID: p.TestCaseID,
		Status: p.Status, DurationMS: p.DurationMS, Notes: p.Notes, RunBy: &p.RunBy}
	err := s.pool.QueryRow(ctx, `INSERT INTO test_runs
		(id, test_case_id, status, duration_ms, notes, run_by)
		SELECT $1,$2,$3,$4,$5,$6 WHERE EXISTS (
			SELECT 1 FROM test_cases tc JOIN test_suites ts ON ts.id = tc.suite_id
			JOIN test_plans tp ON tp.id = ts.plan_id
			WHERE tc.id = $2 AND tp.project_id = $7
		) RETURNING run_at`, out.ID, out.TestCaseID, out.Status, out.DurationMS,
		out.Notes, out.RunBy, projectID).Scan(&out.RunAt)
	if err != nil {
		return nil, mapDBError(err, "test case")
	}
	return out, nil
}

// --- artifact catalogue and releases --------------------------------------------

type CreatePackageParams struct {
	ProjectID   uuid.UUID
	Kind        string
	Name        string
	Description string
}

func (s *Store) CreatePackage(ctx context.Context, p CreatePackageParams) (*model.ArtifactPackage, error) {
	out := &model.ArtifactPackage{ID: uuid.New(), ProjectID: p.ProjectID,
		Kind: p.Kind, Name: strings.TrimSpace(p.Name), Description: p.Description}
	err := s.pool.QueryRow(ctx, `INSERT INTO artifact_packages
		(id, project_id, kind, name, description) VALUES ($1,$2,$3,$4,$5)
		RETURNING created_at, updated_at`, out.ID, out.ProjectID, out.Kind,
		out.Name, out.Description).Scan(&out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, mapDBError(err, "package")
	}
	return out, nil
}

func (s *Store) ListPackages(ctx context.Context, projectID uuid.UUID) ([]model.ArtifactPackage, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id, p.project_id, p.kind, p.name,
		p.description, p.created_at, p.updated_at, count(v.id)
		FROM artifact_packages p LEFT JOIN artifact_package_versions v ON v.package_id = p.id
		WHERE p.project_id = $1 GROUP BY p.id ORDER BY p.updated_at DESC`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.ArtifactPackage, 0)
	for rows.Next() {
		var item model.ArtifactPackage
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Kind, &item.Name,
			&item.Description, &item.CreatedAt, &item.UpdatedAt, &item.Versions); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err(), "packages")
}

type PublishVersionParams struct {
	ProjectID   uuid.UUID
	PackageID   uuid.UUID
	Version     string
	SizeBytes   int64
	SHA256      string
	ContentType string
	Metadata    json.RawMessage
	PublishedBy uuid.UUID
}

func (s *Store) PublishVersion(ctx context.Context, p PublishVersionParams) (*model.PackageVersion, error) {
	if len(p.Metadata) == 0 {
		p.Metadata = json.RawMessage("{}")
	}
	out := &model.PackageVersion{ID: uuid.New(), PackageID: p.PackageID,
		Version: strings.TrimSpace(p.Version), BlobKey: formatBlobKey(p.ProjectID, p.PackageID, p.Version),
		SizeBytes: p.SizeBytes, SHA256: strings.ToLower(strings.TrimSpace(p.SHA256)),
		ContentType: p.ContentType, Metadata: p.Metadata, PublishedBy: &p.PublishedBy}
	if out.ContentType == "" {
		out.ContentType = "application/octet-stream"
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO artifact_package_versions
		(id, package_id, version, blob_key, size_bytes, sha256, content_type, metadata, published_by)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9
		WHERE EXISTS (SELECT 1 FROM artifact_packages WHERE id = $2 AND project_id = $10)
		RETURNING published_at`, out.ID, out.PackageID, out.Version, out.BlobKey,
		out.SizeBytes, out.SHA256, out.ContentType, out.Metadata, out.PublishedBy,
		p.ProjectID).Scan(&out.PublishedAt)
	if err != nil {
		return nil, mapDBError(err, "package version")
	}
	return out, nil
}

func (s *Store) ListVersions(ctx context.Context, projectID, packageID uuid.UUID) ([]model.PackageVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT v.id, v.package_id, v.version, v.blob_key,
		v.size_bytes, v.sha256, v.content_type, v.metadata, v.published_by, v.published_at
		FROM artifact_package_versions v JOIN artifact_packages p ON p.id = v.package_id
		WHERE p.project_id = $1 AND v.package_id = $2 ORDER BY v.published_at DESC`, projectID, packageID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.PackageVersion, 0)
	for rows.Next() {
		var item model.PackageVersion
		if err := rows.Scan(&item.ID, &item.PackageID, &item.Version, &item.BlobKey,
			&item.SizeBytes, &item.SHA256, &item.ContentType, &item.Metadata,
			&item.PublishedBy, &item.PublishedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err(), "package versions")
}

type CreateReleaseParams struct {
	ProjectID  uuid.UUID
	TagName    string
	Name       string
	Notes      string
	Prerelease bool
	CreatedBy  uuid.UUID
	Publish    bool
}

func (s *Store) CreateRelease(ctx context.Context, p CreateReleaseParams) (*model.ProjectRelease, error) {
	out := &model.ProjectRelease{ID: uuid.New(), ProjectID: p.ProjectID,
		TagName: strings.TrimSpace(p.TagName), Name: strings.TrimSpace(p.Name),
		Notes: p.Notes, Prerelease: p.Prerelease, CreatedBy: &p.CreatedBy}
	err := s.pool.QueryRow(ctx, `INSERT INTO project_releases
		(id, project_id, tag_name, name, notes, prerelease, created_by, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,CASE WHEN $8 THEN now() ELSE NULL END)
		RETURNING created_at, published_at`, out.ID, out.ProjectID, out.TagName,
		out.Name, out.Notes, out.Prerelease, out.CreatedBy, p.Publish).
		Scan(&out.CreatedAt, &out.PublishedAt)
	if err != nil {
		return nil, mapDBError(err, "release")
	}
	return out, nil
}

func (s *Store) ListReleases(ctx context.Context, projectID uuid.UUID) ([]model.ProjectRelease, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, project_id, tag_name, name, notes,
		prerelease, created_by, created_at, published_at FROM project_releases
		WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.ProjectRelease, 0)
	for rows.Next() {
		var item model.ProjectRelease
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.TagName, &item.Name,
			&item.Notes, &item.Prerelease, &item.CreatedBy, &item.CreatedAt,
			&item.PublishedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err(), "releases")
}

// --- repository settings --------------------------------------------------------

func (s *Store) GetRepoSettings(ctx context.Context, repoID uuid.UUID) (*model.RepoSettings, error) {
	var out model.RepoSettings
	err := s.pool.QueryRow(ctx, `SELECT id, default_branch, topics, issues_enabled,
		wiki_enabled, merge_strategies, delete_branch_on_merge FROM repos WHERE id = $1`,
		repoID).Scan(&out.RepoID, &out.DefaultBranch, &out.Topics,
		&out.IssuesEnabled, &out.WikiEnabled, &out.MergeStrategies,
		&out.DeleteBranchOnMerge)
	if err != nil {
		return nil, mapDBError(err, "repository")
	}
	return &out, nil
}

type UpdateRepoSettingsParams struct {
	DefaultBranch       *string
	Topics              *[]string
	IssuesEnabled       *bool
	WikiEnabled         *bool
	MergeStrategies     *[]string
	DeleteBranchOnMerge *bool
}

func (s *Store) UpdateRepoSettings(ctx context.Context, repoID uuid.UUID, p UpdateRepoSettingsParams) (*model.RepoSettings, error) {
	if p.Topics != nil {
		normalized := normalizedList(*p.Topics)
		p.Topics = &normalized
	}
	if p.MergeStrategies != nil {
		normalized := normalizedList(*p.MergeStrategies)
		if !validMergeStrategies(normalized) {
			return nil, apperr.Validation("merge_strategies must contain merge, squash, or rebase", nil)
		}
		p.MergeStrategies = &normalized
	}
	var out model.RepoSettings
	err := s.pool.QueryRow(ctx, `UPDATE repos SET
		default_branch = COALESCE($2, default_branch), topics = COALESCE($3, topics),
		issues_enabled = COALESCE($4, issues_enabled), wiki_enabled = COALESCE($5, wiki_enabled),
		merge_strategies = COALESCE($6, merge_strategies),
		delete_branch_on_merge = COALESCE($7, delete_branch_on_merge), updated_at = now()
		WHERE id = $1 RETURNING id, default_branch, topics, issues_enabled,
		wiki_enabled, merge_strategies, delete_branch_on_merge`, repoID, p.DefaultBranch,
		p.Topics, p.IssuesEnabled, p.WikiEnabled, p.MergeStrategies,
		p.DeleteBranchOnMerge).Scan(&out.RepoID, &out.DefaultBranch, &out.Topics,
		&out.IssuesEnabled, &out.WikiEnabled, &out.MergeStrategies,
		&out.DeleteBranchOnMerge)
	if err != nil {
		return nil, mapDBError(err, "repository settings")
	}
	return &out, nil
}
