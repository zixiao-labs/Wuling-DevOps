package pipelinestore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
)

// AcquiredJob is everything a runner needs to execute a job, returned by
// AcquireJob. The HTTP layer enriches it with decrypted secrets and a checkout
// token before sending it to the runner — those never live in the store.
type AcquiredJob struct {
	JobID       uuid.UUID            `json:"job_id"`
	RunID       uuid.UUID            `json:"run_id"`
	RunNumber   int64                `json:"run_number"`
	OrgID       uuid.UUID            `json:"-"`
	ProjectID   uuid.UUID            `json:"-"`
	RepoID      uuid.UUID            `json:"-"`
	OrgSlug     string               `json:"org_slug"`
	ProjectSlug string               `json:"project_slug"`
	RepoSlug    string               `json:"repo_slug"`
	JobName     string               `json:"job_name"`
	CommitSHA   string               `json:"commit_sha"`
	GitRef      string               `json:"git_ref"`
	Event       string               `json:"event"`
	Spec        pipeline.JobSpec     `json:"spec"`
	Steps       []model.PipelineStep `json:"steps"`
}

// AcquireJob atomically claims the oldest dispatchable job for a runner.
// "Dispatchable" = queued, tier matches the runner exactly, the job's runs-on
// labels are a subset of the runner's labels, and every `needs` dependency has
// succeeded. The runner's labels/tier are read authoritatively from its row
// (never trusted from the request) so a runner can't grab work it isn't sized
// for. Returns (nil, nil) when nothing matches — the runner long-polls again.
// FOR UPDATE OF j SKIP LOCKED lets concurrent runners scan past each other's
// claimed rows without blocking.
func (s *Store) AcquireJob(ctx context.Context, runnerID uuid.UUID) (*AcquiredJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read the runner's org + match spec authoritatively from its row.
	var (
		orgID  uuid.UUID
		labels []string
		tier   string
	)
	if err := tx.QueryRow(ctx,
		`SELECT org_id, labels, resource_tier FROM runners WHERE id = $1`, runnerID).
		Scan(&orgID, &labels, &tier); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Unauthorized("unknown runner")
		}
		return nil, apperr.Internal(err)
	}

	var (
		aj       AcquiredJob
		specJSON []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT j.id, j.run_id, j.name, j.definition,
		       run.number, run.project_id, run.repo_id, run.commit_sha, run.git_ref, run.event,
		       o.slug, p.slug, rp.slug
		FROM pipeline_jobs j
		JOIN pipeline_runs run ON run.id = j.run_id
		JOIN orgs o   ON o.id  = j.org_id
		JOIN projects p ON p.id = run.project_id
		JOIN repos rp ON rp.id = run.repo_id
		WHERE j.org_id = $1
		  AND j.status = 'queued'
		  AND j.resource_tier = $3
		  AND j.runs_on <@ $2::text[]
		  AND NOT EXISTS (
		        SELECT 1 FROM unnest(j.needs) AS need
		        WHERE NOT EXISTS (
		          SELECT 1 FROM pipeline_jobs d
		          WHERE d.run_id = j.run_id AND d.name = need AND d.status = 'success'
		        )
		      )
		ORDER BY j.queued_at ASC
		FOR UPDATE OF j SKIP LOCKED
		LIMIT 1
	`, orgID, normStrings(labels), tier).Scan(
		&aj.JobID, &aj.RunID, &aj.JobName, &specJSON,
		&aj.RunNumber, &aj.ProjectID, &aj.RepoID, &aj.CommitSHA, &aj.GitRef, &aj.Event,
		&aj.OrgSlug, &aj.ProjectSlug, &aj.RepoSlug,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // nothing to do
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	aj.OrgID = orgID
	if err := json.Unmarshal(specJSON, &aj.Spec); err != nil {
		return nil, apperr.Internal(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_jobs SET status = 'running', runner_id = $2, started_at = now()
		WHERE id = $1
	`, aj.JobID, runnerID); err != nil {
		return nil, apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_runs SET status = 'running', started_at = COALESCE(started_at, now())
		WHERE id = $1 AND status = 'queued'
	`, aj.RunID); err != nil {
		return nil, apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE runners SET status = 'busy', last_seen_at = now() WHERE id = $1
	`, runnerID); err != nil {
		return nil, apperr.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}

	steps, err := s.listSteps(ctx, aj.JobID)
	if err != nil {
		return nil, err
	}
	aj.Steps = steps
	return &aj, nil
}

// UpdateStepParams patches one step's status. Step is addressed by (job,
// number). Status transitions are validated by the CHECK constraint.
type UpdateStepParams struct {
	JobID  uuid.UUID
	Number int
	Status string
}

// UpdateStep records a step status change reported by a runner, stamping
// started_at/finished_at on the appropriate transitions.
func (s *Store) UpdateStep(ctx context.Context, p UpdateStepParams) error {
	var startExpr, finishExpr string
	switch p.Status {
	case "running":
		startExpr = "started_at = COALESCE(started_at, now())"
	case "success", "failed", "canceled", "skipped":
		finishExpr = "finished_at = now()"
	}
	q := "UPDATE pipeline_steps SET status = $3"
	if startExpr != "" {
		q += ", " + startExpr
	}
	if finishExpr != "" {
		q += ", " + finishExpr
	}
	q += " WHERE job_id = $1 AND number = $2"
	tag, err := s.pool.Exec(ctx, q, p.JobID, p.Number, p.Status)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("step")
	}
	return nil
}

// CompleteJob finalizes a job with conclusion (success|failed|canceled), then:
//   - frees the runner (idle, stamps last_job_at for idle scale-down),
//   - cascades cancellation to queued jobs whose needs can no longer succeed,
//   - re-aggregates the run's status, stamping finished_at once all jobs end.
func (s *Store) CompleteJob(ctx context.Context, jobID uuid.UUID, conclusion string) error {
	switch conclusion {
	case "success", "failed", "canceled":
	default:
		return apperr.Validation("conclusion must be success|failed|canceled", nil)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var runID uuid.UUID
	var runnerID *uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE pipeline_jobs SET status = $2, finished_at = now()
		WHERE id = $1 AND status = 'running'
		RETURNING run_id, runner_id
	`, jobID, conclusion).Scan(&runID, &runnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(apperr.CodeConflict, "job is not running")
	}
	if err != nil {
		return apperr.Internal(err)
	}

	if runnerID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE runners SET status = 'idle', last_job_at = now(), last_seen_at = now() WHERE id = $1`,
			*runnerID); err != nil {
			return apperr.Internal(err)
		}
	}

	// Cascade-cancel queued jobs whose needs include a failed/canceled job.
	// Looping handles transitive chains (A→B→C): canceling B then cancels C.
	for {
		tag, err := tx.Exec(ctx, `
			UPDATE pipeline_jobs j SET status = 'canceled', finished_at = now()
			WHERE j.run_id = $1 AND j.status = 'queued'
			  AND EXISTS (
			        SELECT 1 FROM unnest(j.needs) AS need
			        JOIN pipeline_jobs d ON d.run_id = j.run_id AND d.name = need
			        WHERE d.status IN ('failed', 'canceled')
			      )
		`, runID)
		if err != nil {
			return apperr.Internal(err)
		}
		if tag.RowsAffected() == 0 {
			break
		}
	}

	// Re-aggregate run status.
	var pending, failed, canceled int
	if err := tx.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE status NOT IN ('success','failed','canceled')),
		  COUNT(*) FILTER (WHERE status = 'failed'),
		  COUNT(*) FILTER (WHERE status = 'canceled')
		FROM pipeline_jobs WHERE run_id = $1
	`, runID).Scan(&pending, &failed, &canceled); err != nil {
		return apperr.Internal(err)
	}
	if pending == 0 {
		runStatus := "success"
		switch {
		case failed > 0:
			runStatus = "failed"
		case canceled > 0:
			runStatus = "canceled"
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pipeline_runs SET status = $2, finished_at = now() WHERE id = $1`,
			runID, runStatus); err != nil {
			return apperr.Internal(err)
		}
	}

	return tx.Commit(ctx)
}

// CancelRun marks a run and its non-terminal jobs/steps canceled. Running jobs
// are cut short — the runner discovers the cancellation on its next callback
// (which returns a conflict) and aborts.
func (s *Store) CancelRun(ctx context.Context, runID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE pipeline_runs SET status = 'canceled', finished_at = now()
		WHERE id = $1 AND status IN ('queued','running')
	`, runID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(apperr.CodeConflict, "run is already finished")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_jobs SET status = 'canceled', finished_at = now()
		WHERE run_id = $1 AND status IN ('queued','running')
	`, runID); err != nil {
		return apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_steps st SET status = 'canceled', finished_at = now()
		FROM pipeline_jobs j
		WHERE st.job_id = j.id AND j.run_id = $1 AND st.status IN ('queued','running')
	`, runID); err != nil {
		return apperr.Internal(err)
	}
	return tx.Commit(ctx)
}

// RequeueStaleJobs finds jobs whose runner has gone silent (or vanished) past
// reapAfter and either requeues them (attempt++) or, once MaxJobAttempts is
// exceeded, fails them. Returns the number of jobs acted on. Meant to be
// called periodically by the control plane.
func (s *Store) RequeueStaleJobs(ctx context.Context, reapAfter time.Duration) (int, error) {
	cutoff := time.Now().Add(-reapAfter)
	rows, err := s.pool.Query(ctx, `
		SELECT j.id, j.attempt
		FROM pipeline_jobs j
		LEFT JOIN runners r ON r.id = j.runner_id
		WHERE j.status = 'running'
		  AND (r.id IS NULL OR r.last_seen_at IS NULL OR r.last_seen_at < $1)
	`, cutoff)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	type stale struct {
		id      uuid.UUID
		attempt int
	}
	var list []stale
	for rows.Next() {
		var st stale
		if err := rows.Scan(&st.id, &st.attempt); err != nil {
			rows.Close()
			return 0, apperr.Internal(err)
		}
		list = append(list, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, apperr.Internal(err)
	}

	acted := 0
	for _, st := range list {
		if st.attempt >= MaxJobAttempts {
			if err := s.CompleteJob(ctx, st.id, "failed"); err != nil {
				return acted, err
			}
		} else {
			if _, err := s.pool.Exec(ctx, `
				UPDATE pipeline_jobs
				SET status = 'queued', runner_id = NULL, started_at = NULL, attempt = attempt + 1
				WHERE id = $1 AND status = 'running'
			`, st.id); err != nil {
				return acted, apperr.Internal(err)
			}
		}
		acted++
	}
	return acted, nil
}

// QueuedJob is one unit of pending demand, used by the autoscaler to decide
// what to launch.
type QueuedJob struct {
	Tier   string
	RunsOn []string
}

// QueuedDemand returns the tier/labels of every queued, dependency-satisfied
// job in an org — i.e. work that could run right now if a matching runner
// existed. The autoscaler diffs this against online runners to size pools.
func (s *Store) QueuedDemand(ctx context.Context, orgID uuid.UUID) ([]QueuedJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT j.resource_tier, j.runs_on
		FROM pipeline_jobs j
		WHERE j.org_id = $1 AND j.status = 'queued'
		  AND NOT EXISTS (
		        SELECT 1 FROM unnest(j.needs) AS need
		        WHERE NOT EXISTS (
		          SELECT 1 FROM pipeline_jobs d
		          WHERE d.run_id = j.run_id AND d.name = need AND d.status = 'success'
		        )
		      )
	`, orgID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]QueuedJob, 0)
	for rows.Next() {
		var q QueuedJob
		if err := rows.Scan(&q.Tier, &q.RunsOn); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// OrgsWithQueuedJobs lists orgs that have at least one queued job. The
// autoscaler unions this with orgs that have ephemeral runners to decide which
// orgs to reconcile each tick.
func (s *Store) OrgsWithQueuedJobs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT org_id FROM pipeline_jobs WHERE status = 'queued'`)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
