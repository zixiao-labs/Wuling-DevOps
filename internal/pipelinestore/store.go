// Package pipelinestore is the persistence layer for Pipelines: runs, jobs,
// steps, the dispatch queue, and run-status aggregation. It never imports the
// HTTP layer. Job logs are disk-backed (see logs.go); this file owns the
// relational state.
//
// Dispatch is the hot path: a runner long-polls AcquireJob, which atomically
// picks the oldest queued job whose labels ⊆ the runner's, whose tier matches,
// and whose `needs` are all satisfied, using FOR UPDATE SKIP LOCKED so many
// runners never collide on one job.
package pipelinestore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
)

// MaxJobAttempts caps how many times a stale job is requeued before it is
// failed outright — guards against a poison job cycling forever.
const MaxJobAttempts = 3

// Store is the data-access object for Pipelines.
type Store struct {
	pool   *db.Pool
	logDir string
}

// New returns a Store. logDir is where job logs are appended on disk.
func New(pool *db.Pool, logDir string) *Store {
	return &Store{pool: pool, logDir: logDir}
}

// ----------------------------------------------------------------------------
// run creation
// ----------------------------------------------------------------------------

// CreateRunParams holds inputs to CreateRun.
type CreateRunParams struct {
	OrgID         uuid.UUID
	ProjectID     uuid.UUID
	RepoID        uuid.UUID
	WorkflowPath  string
	Event         string // push | pull_request | manual
	GitRef        string
	CommitSHA     string
	CommitMessage string
	TriggeredBy   uuid.UUID // uuid.Nil for system
	Workflow      *pipeline.Workflow
	DefaultTier   string
}

// CreateRun materializes a parsed workflow into a run + its jobs + steps in one
// transaction, allocating the per-repo run number. Jobs start 'queued'; the
// dispatch query gates them on `needs` so they only become acquirable once
// their dependencies succeed.
func (s *Store) CreateRun(ctx context.Context, p CreateRunParams) (*model.PipelineRun, error) {
	if p.Workflow == nil {
		return nil, apperr.Validation("workflow is required", nil)
	}
	order, err := p.Workflow.JobOrder()
	if err != nil {
		return nil, apperr.Validation(err.Error(), nil)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var number int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO pipeline_run_number_seq (repo_id, next_value)
		VALUES ($1, 2)
		ON CONFLICT (repo_id) DO UPDATE
		   SET next_value = pipeline_run_number_seq.next_value + 1
		RETURNING next_value - 1
	`, p.RepoID).Scan(&number); err != nil {
		return nil, apperr.Internal(err)
	}

	defJSON, err := json.Marshal(p.Workflow)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	run := &model.PipelineRun{
		ID:            uuid.New(),
		OrgID:         p.OrgID,
		ProjectID:     p.ProjectID,
		RepoID:        p.RepoID,
		Number:        number,
		WorkflowPath:  p.WorkflowPath,
		WorkflowName:  p.Workflow.Name,
		Event:         p.Event,
		GitRef:        p.GitRef,
		CommitSHA:     p.CommitSHA,
		CommitMessage: p.CommitMessage,
		Status:        "queued",
	}
	var triggeredBy any
	if p.TriggeredBy != uuid.Nil {
		triggeredBy = p.TriggeredBy
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO pipeline_runs
		    (id, org_id, project_id, repo_id, number, workflow_path, workflow_name,
		     event, git_ref, commit_sha, commit_message, status, triggered_by, definition)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'queued',$12,$13::jsonb)
		RETURNING created_at
	`, run.ID, run.OrgID, run.ProjectID, run.RepoID, run.Number, run.WorkflowPath, run.WorkflowName,
		run.Event, run.GitRef, run.CommitSHA, run.CommitMessage, triggeredBy, string(defJSON)).
		Scan(&run.CreatedAt); err != nil {
		return nil, apperr.Internal(err)
	}

	for _, name := range order {
		job := p.Workflow.Jobs[name]
		spec := job.Spec()
		specJSON, err := json.Marshal(spec)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		jobID := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO pipeline_jobs
			    (id, run_id, org_id, name, runs_on, resource_tier, needs, status, definition)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'queued',$8::jsonb)
		`, jobID, run.ID, p.OrgID, name, normStrings(job.RunsOn), job.EffectiveTier(p.DefaultTier),
			normStrings(job.Needs), string(specJSON)); err != nil {
			return nil, apperr.Internal(err)
		}
		for i, st := range spec.Steps {
			if _, err := tx.Exec(ctx, `
				INSERT INTO pipeline_steps (id, job_id, number, name, status)
				VALUES ($1,$2,$3,$4,'queued')
			`, uuid.New(), jobID, i+1, st.StepDisplayName()); err != nil {
				return nil, apperr.Internal(err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	return s.GetRun(ctx, run.ID)
}

// ----------------------------------------------------------------------------
// reads
// ----------------------------------------------------------------------------

// ListRunsFilter narrows ListRuns.
type ListRunsFilter struct {
	RepoID uuid.UUID // uuid.Nil = all repos in project
	Status string    // "" = any
	Limit  int
}

// ListRunsByProject returns runs in a project, newest first.
func (s *Store) ListRunsByProject(ctx context.Context, projectID uuid.UUID, f ListRunsFilter) ([]model.PipelineRun, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args := []any{projectID}
	q := `
		SELECT r.id, r.org_id, r.project_id, r.repo_id, r.number, r.workflow_path, r.workflow_name,
		       r.event, r.git_ref, r.commit_sha, r.commit_message, r.status,
		       r.created_at, r.started_at, r.finished_at,
		       u.id, u.username, u.display_name
		FROM pipeline_runs r
		LEFT JOIN users u ON u.id = r.triggered_by
		WHERE r.project_id = $1`
	if f.RepoID != uuid.Nil {
		args = append(args, f.RepoID)
		q += " AND r.repo_id = $2"
	}
	if f.Status != "" {
		args = append(args, f.Status)
		q += " AND r.status = $" + itoa(len(args))
	}
	args = append(args, limit)
	q += " ORDER BY r.created_at DESC LIMIT $" + itoa(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	return scanRuns(rows)
}

// GetRun returns a run with its jobs (steps omitted; use GetJob for steps).
func (s *Store) GetRun(ctx context.Context, runID uuid.UUID) (*model.PipelineRun, error) {
	run, err := s.getRunRow(ctx, "r.id = $1", runID)
	if err != nil {
		return nil, err
	}
	jobs, err := s.listJobs(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	run.Jobs = jobs
	return run, nil
}

// GetRunByNumber returns a run by its (repo, number) identity, with jobs+steps.
func (s *Store) GetRunByNumber(ctx context.Context, repoID uuid.UUID, number int64) (*model.PipelineRun, error) {
	return s.runWithSteps(ctx, "r.repo_id = $1 AND r.number = $2", repoID, number)
}

// GetRunWithSteps returns a run by id with jobs+steps eagerly attached, for the
// run-detail view — a single run/jobs/steps load (no redundant get-then-get).
func (s *Store) GetRunWithSteps(ctx context.Context, runID uuid.UUID) (*model.PipelineRun, error) {
	return s.runWithSteps(ctx, "r.id = $1", runID)
}

func (s *Store) runWithSteps(ctx context.Context, where string, args ...any) (*model.PipelineRun, error) {
	run, err := s.getRunRow(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	jobs, err := s.listJobs(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	// eagerly attach steps for the detail view
	for i := range jobs {
		steps, err := s.listSteps(ctx, jobs[i].ID)
		if err != nil {
			return nil, err
		}
		jobs[i].Steps = steps
	}
	run.Jobs = jobs
	return run, nil
}

func (s *Store) getRunRow(ctx context.Context, where string, args ...any) (*model.PipelineRun, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT r.id, r.org_id, r.project_id, r.repo_id, r.number, r.workflow_path, r.workflow_name,
		       r.event, r.git_ref, r.commit_sha, r.commit_message, r.status,
		       r.created_at, r.started_at, r.finished_at,
		       u.id, u.username, u.display_name
		FROM pipeline_runs r
		LEFT JOIN users u ON u.id = r.triggered_by
		WHERE `+where, args...)
	run, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("pipeline run")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return run, nil
}

func (s *Store) listJobs(ctx context.Context, runID uuid.UUID) ([]model.PipelineJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, name, runs_on, resource_tier, needs, status, runner_id, attempt, log_size, queued_at, started_at, finished_at
		FROM pipeline_jobs WHERE run_id = $1 ORDER BY queued_at ASC, name ASC
	`, runID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.PipelineJob, 0)
	for rows.Next() {
		var j model.PipelineJob
		if err := rows.Scan(&j.ID, &j.RunID, &j.Name, &j.RunsOn, &j.ResourceTier, &j.Needs,
			&j.Status, &j.RunnerID, &j.Attempt, &j.LogSize, &j.QueuedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

func (s *Store) listSteps(ctx context.Context, jobID uuid.UUID) ([]model.PipelineStep, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, job_id, number, name, status, started_at, finished_at
		FROM pipeline_steps WHERE job_id = $1 ORDER BY number ASC
	`, jobID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.PipelineStep, 0)
	for rows.Next() {
		var st model.PipelineStep
		if err := rows.Scan(&st.ID, &st.JobID, &st.Number, &st.Name, &st.Status, &st.StartedAt, &st.FinishedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// JobContext is the authoritative org/project/repo scope of a job, used by the
// HTTP layer to authorize runner callbacks and build the checkout URL.
type JobContext struct {
	JobID       uuid.UUID
	RunID       uuid.UUID
	OrgID       uuid.UUID
	ProjectID   uuid.UUID
	RepoID      uuid.UUID
	RunnerID    *uuid.UUID
	Status      string
	OrgSlug     string
	ProjectSlug string
	RepoSlug    string
	CommitSHA   string
}

// JobCtx loads a job's scope. Returns NotFound if the job doesn't exist.
func (s *Store) JobCtx(ctx context.Context, jobID uuid.UUID) (*JobContext, error) {
	var jc JobContext
	jc.JobID = jobID
	err := s.pool.QueryRow(ctx, `
		SELECT j.run_id, j.org_id, run.project_id, run.repo_id, j.runner_id, j.status,
		       o.slug, p.slug, rp.slug, run.commit_sha
		FROM pipeline_jobs j
		JOIN pipeline_runs run ON run.id = j.run_id
		JOIN orgs o ON o.id = j.org_id
		JOIN projects p ON p.id = run.project_id
		JOIN repos rp ON rp.id = run.repo_id
		WHERE j.id = $1
	`, jobID).Scan(&jc.RunID, &jc.OrgID, &jc.ProjectID, &jc.RepoID, &jc.RunnerID, &jc.Status,
		&jc.OrgSlug, &jc.ProjectSlug, &jc.RepoSlug, &jc.CommitSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("job")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &jc, nil
}
