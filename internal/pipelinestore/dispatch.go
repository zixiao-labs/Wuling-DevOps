package pipelinestore

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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
	JobKey      string               `json:"job_key"`
	Matrix      map[string]string    `json:"matrix,omitempty"`
	CommitSHA   string               `json:"commit_sha"`
	GitRef      string               `json:"git_ref"`
	Event       string               `json:"event"`
	Spec        pipeline.JobSpec     `json:"spec"`
	Steps       []model.PipelineStep `json:"steps"`
}

// dispatchableNeedsSQL is the shared "every needed job has at least one leg and
// no non-success leg" gate, aliased on `j`. A matrix job expands to many rows
// sharing one job_key, so `needs` resolves against job_key with all-legs-must-
// succeed semantics — matching by name would look for a row called "build" and
// never find "build (ubuntu, 18)", stranding every dependent forever.
//
// Kept in one place because AcquireJob, CompleteJob and QueuedDemand must agree
// on what is runnable, or the autoscaler provisions for work the dispatcher
// will not hand out. For a single-leg job this is exactly equivalent to the
// pre-matrix `(>=1 success)` predicate, which is what makes the 0012 backfill
// (job_key = name) safe for runs already in flight.
//
// The "zero legs" arm is defensive: Validate rejects an unknown `need` and
// Combinations rejects an empty matrix, so it should be unreachable.
const dispatchableNeedsSQL = `
		NOT EXISTS (
		  SELECT 1 FROM unnest(j.needs) AS need
		  WHERE NOT EXISTS (SELECT 1 FROM pipeline_jobs d
		                    WHERE d.run_id = j.run_id AND d.job_key = need)
		     OR EXISTS     (SELECT 1 FROM pipeline_jobs d
		                    WHERE d.run_id = j.run_id AND d.job_key = need AND d.status <> 'success')
		)`

// maxAcquireAttempts bounds how many matrix groups one acquire call will skip
// past after losing a max-parallel admission race before giving up and letting
// the runner long-poll again.
const maxAcquireAttempts = 3

// AcquireJob atomically claims the oldest dispatchable job for a runner.
// "Dispatchable" = queued, tier matches the runner exactly, the job's runs-on
// labels are a subset of the runner's labels, every `needs` dependency has
// succeeded in all its legs, and the job's matrix group is under its
// max-parallel cap. An isolated job must first be reserved for this exact
// runner; an exclusive job is admitted only when the runner has no running
// work, and blocks additional claims while it runs. The runner's labels/tier
// are read authoritatively from its locked row (never trusted from the request)
// so a runner can't grab work it isn't sized for. Returns (nil, nil) when
// nothing matches — the runner long-polls again. FOR UPDATE OF j SKIP LOCKED
// lets concurrent runners scan past each other's claimed rows without
// blocking.
//
// A lost max-parallel race retries with the throttled group excluded rather
// than returning empty: the inner SELECT is LIMIT 1, so a throttled group at
// the head of the queue would otherwise stall the runner for a full poll
// interval even when unrelated runs have work.
func (s *Store) AcquireJob(ctx context.Context, runnerID uuid.UUID) (*AcquiredJob, error) {
	var throttled []string
	for i := 0; i < maxAcquireAttempts; i++ {
		aj, blockedKey, err := s.acquireOnce(ctx, runnerID, throttled)
		if err != nil {
			return nil, err
		}
		if aj != nil {
			return aj, nil
		}
		if blockedKey == "" {
			return nil, nil // nothing dispatchable
		}
		throttled = append(throttled, blockedKey)
	}
	return nil, nil
}

// acquireOnce is one claim attempt in its own transaction. It returns the
// claimed job, or the job_key of a matrix group that lost the max-parallel
// admission race (so the caller can retry past it), or neither when the queue
// has nothing for this runner.
func (s *Store) acquireOnce(ctx context.Context, runnerID uuid.UUID, excludeKeys []string) (*AcquiredJob, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the runner for the entire admission decision. Every acquisition for
	// this runner, including shared work, takes this lock so count-then-claim
	// for exclusive work cannot race on distinct pipeline_jobs rows.
	var (
		orgID         uuid.UUID
		labels        []string
		tier          string
		poolName      string
		isolatedJobID *uuid.UUID
	)
	if err := tx.QueryRow(ctx,
		`SELECT org_id, labels, resource_tier, pool_name, isolated_job_id FROM runners WHERE id = $1 FOR UPDATE`, runnerID).
		Scan(&orgID, &labels, &tier, &poolName, &isolatedJobID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", apperr.Unauthorized("unknown runner")
		}
		return nil, "", apperr.Internal(err)
	}

	var runningJobs, runningExclusive int
	if err := tx.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE execution_mode = 'exclusive')
		FROM pipeline_jobs
		WHERE runner_id = $1 AND status = 'running'
	`, runnerID).Scan(&runningJobs, &runningExclusive); err != nil {
		return nil, "", apperr.Internal(err)
	}

	var (
		aj          AcquiredJob
		specJSON    []byte
		matrixRaw   []byte
		maxParallel int
	)
	err = tx.QueryRow(ctx, `
		SELECT j.id, j.run_id, j.name, j.job_key, j.matrix, j.max_parallel, j.definition,
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
		  AND `+dispatchableNeedsSQL+`
		  AND (j.max_parallel = 0 OR j.job_key <> ALL($4::text[]))
		  AND (j.max_parallel = 0 OR (
		        SELECT count(*) FROM pipeline_jobs sib
		        WHERE sib.run_id = j.run_id AND sib.job_key = j.job_key AND sib.status = 'running'
		      ) < j.max_parallel)
		  AND (
		        (j.execution_mode = 'isolated'
		         AND j.reserved_runner_id = $5
		         AND j.execution_pool = $6
		         AND j.id = $9)
		        OR
		        (j.execution_mode <> 'isolated' AND $9 IS NULL)
		      )
		  AND (j.execution_mode <> 'exclusive' OR $7 = 0)
		  AND $8 = 0
		ORDER BY CASE
		           WHEN j.execution_mode = 'isolated' AND j.reserved_runner_id = $5 THEN 0
		           ELSE 1
		         END,
		         j.queued_at ASC, j.ordinal ASC, j.name ASC
		FOR UPDATE OF j SKIP LOCKED
		LIMIT 1
	`, orgID, normStrings(labels), tier, normStrings(excludeKeys), runnerID, poolName,
		runningJobs, runningExclusive, isolatedJobID).Scan(
		&aj.JobID, &aj.RunID, &aj.JobName, &aj.JobKey, &matrixRaw, &maxParallel, &specJSON,
		&aj.RunNumber, &aj.ProjectID, &aj.RepoID, &aj.CommitSHA, &aj.GitRef, &aj.Event,
		&aj.OrgSlug, &aj.ProjectSlug, &aj.RepoSlug,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil // nothing to do
	}
	if err != nil {
		return nil, "", apperr.Internal(err)
	}
	aj.OrgID = orgID
	if err := json.Unmarshal(specJSON, &aj.Spec); err != nil {
		return nil, "", apperr.Internal(err)
	}
	if err := decodeMatrix(matrixRaw, &aj.Matrix); err != nil {
		return nil, "", err
	}

	// max-parallel is a GROUP invariant, but SKIP LOCKED only guards the single
	// row we picked: two runners scanning two different legs of one group lock
	// disjoint rows, both read the same running-count under READ COMMITTED, and
	// both admit. Serialise admissions within one matrix group so count-then-
	// claim is atomic. pg_advisory_xact_lock releases on commit/rollback, and
	// the row lock is always taken before the advisory lock here, so no
	// lock-ordering cycle is reachable.
	if maxParallel > 0 {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			aj.RunID.String()+"/"+aj.JobKey); err != nil {
			return nil, "", apperr.Internal(err)
		}
		var running int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM pipeline_jobs
			WHERE run_id = $1 AND job_key = $2 AND status = 'running'
		`, aj.RunID, aj.JobKey).Scan(&running); err != nil {
			return nil, "", apperr.Internal(err)
		}
		if running >= maxParallel {
			return nil, aj.JobKey, nil // throttled — retry past this group
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_jobs SET status = 'running', runner_id = $2, started_at = now()
		WHERE id = $1
	`, aj.JobID, runnerID); err != nil {
		return nil, "", apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_runs SET status = 'running', started_at = COALESCE(started_at, now())
		WHERE id = $1 AND status = 'queued'
	`, aj.RunID); err != nil {
		return nil, "", apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE runners SET status = 'busy', last_seen_at = now() WHERE id = $1
	`, runnerID); err != nil {
		return nil, "", apperr.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", apperr.Internal(err)
	}

	steps, err := s.listSteps(ctx, aj.JobID)
	if err != nil {
		return nil, "", err
	}
	aj.Steps = steps
	return &aj, "", nil
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
//   - recomputes the runner's busy/idle state from all of its running jobs,
//   - cancels the job's still-live matrix siblings when fail-fast is on,
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
	var jobKey string
	var failFast bool
	err = tx.QueryRow(ctx, `
		UPDATE pipeline_jobs SET status = $2, finished_at = now()
		WHERE id = $1 AND status = 'running'
		RETURNING run_id, runner_id, job_key, fail_fast
	`, jobID, conclusion).Scan(&runID, &runnerID, &jobKey, &failFast)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(apperr.CodeConflict, "job is not running")
	}
	if err != nil {
		return apperr.Internal(err)
	}

	// fail-fast: one failed leg cancels its still-live siblings, before the
	// downstream cascade below picks up the transitive cancellations in the same
	// transaction. A running sibling is flipped in the DB only — its runner
	// discovers it on the next callback, which ownedJob answers 409 for. That is
	// the same mechanism CancelRun relies on.
	if conclusion == "failed" && failFast {
		if err := cancelMatrixSiblings(ctx, tx, runID, jobKey, jobID); err != nil {
			return err
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
			        JOIN pipeline_jobs d ON d.run_id = j.run_id AND d.job_key = need
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

	// A runner can carry multiple shared jobs. Recompute from pipeline_jobs
	// under the same runner row lock AcquireJob uses; setting it idle merely
	// because this one job ended would let an active runner look free.
	if runnerID != nil {
		if err := recomputeRunnerStatuses(ctx, tx, []uuid.UUID{*runnerID}); err != nil {
			return err
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

// cancelMatrixSiblings implements fail-fast: it cancels every still-live leg of
// jobKey other than exceptID, cancels those legs' pending steps, and recomputes
// the runners they were holding. Recomputing matters — an unconditional idle
// update would hide other shared work still running on the same runner.
//
// A leg cancelled while still queued gets finished_at with started_at left
// NULL; that is the same shape CancelRun already produces for queued jobs.
func cancelMatrixSiblings(ctx context.Context, tx pgx.Tx, runID uuid.UUID, jobKey string, exceptID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		UPDATE pipeline_jobs sib SET status = 'canceled', finished_at = now()
		WHERE sib.run_id = $1 AND sib.job_key = $2 AND sib.id <> $3
		  AND sib.status IN ('queued','running')
		RETURNING sib.id, sib.runner_id
	`, runID, jobKey, exceptID)
	if err != nil {
		return apperr.Internal(err)
	}
	var jobIDs, runnerIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var rid *uuid.UUID
		if err := rows.Scan(&id, &rid); err != nil {
			rows.Close()
			return apperr.Internal(err)
		}
		jobIDs = append(jobIDs, id)
		if rid != nil {
			runnerIDs = append(runnerIDs, *rid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return apperr.Internal(err)
	}
	if len(jobIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_steps SET status = 'canceled', finished_at = now()
		WHERE job_id = ANY($1::uuid[]) AND status IN ('queued','running')
	`, jobIDs); err != nil {
		return apperr.Internal(err)
	}
	return recomputeRunnerStatuses(ctx, tx, runnerIDs)
}

// recomputeRunnerStatuses derives each affected runner's status from the
// actual count of running jobs. It locks runner rows in a stable order, which
// both prevents concurrent AcquireJob calls from racing the count and avoids
// inter-run cancellation deadlocks.
func recomputeRunnerStatuses(ctx context.Context, tx pgx.Tx, runnerIDs []uuid.UUID) error {
	seen := make(map[uuid.UUID]struct{}, len(runnerIDs))
	ids := make([]uuid.UUID, 0, len(runnerIDs))
	for _, id := range runnerIDs {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	for _, runnerID := range ids {
		var lockedID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT id FROM runners WHERE id = $1 FOR UPDATE`, runnerID).
			Scan(&lockedID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // the runner was deleted; its job FK is already NULL.
			}
			return apperr.Internal(err)
		}

		var running int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM pipeline_jobs
			WHERE runner_id = $1 AND status = 'running'
		`, lockedID).Scan(&running); err != nil {
			return apperr.Internal(err)
		}
		if running == 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE runners
				SET status = 'idle', last_job_at = now(), last_seen_at = now()
				WHERE id = $1
			`, lockedID); err != nil {
				return apperr.Internal(err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE runners SET status = 'busy', last_seen_at = now() WHERE id = $1
		`, lockedID); err != nil {
			return apperr.Internal(err)
		}
	}
	return nil
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
	// Cancel the non-terminal jobs and collect the runners they were holding.
	// Recomputing their state below scopes the update to these rows while still
	// preserving busy when the runner also owns work from another run.
	rows, err := tx.Query(ctx, `
		UPDATE pipeline_jobs SET status = 'canceled', finished_at = now()
		WHERE run_id = $1 AND status IN ('queued','running')
		RETURNING runner_id
	`, runID)
	if err != nil {
		return apperr.Internal(err)
	}
	var runnerIDs []uuid.UUID
	for rows.Next() {
		var rid *uuid.UUID
		if err := rows.Scan(&rid); err != nil {
			rows.Close()
			return apperr.Internal(err)
		}
		if rid != nil {
			runnerIDs = append(runnerIDs, *rid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return apperr.Internal(err)
	}
	if err := recomputeRunnerStatuses(ctx, tx, runnerIDs); err != nil {
		return err
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
			// Clear reserved_runner_id for isolated jobs so the autoscaler can
			// reclaim the stale VM (reservation-released) and provision a fresh
			// one. Leaving the reservation would permanently pin the job to a
			// dead runner while excluding it from QueuedIsolatedDemand.
			if _, err := s.pool.Exec(ctx, `
				UPDATE pipeline_jobs
				SET status = 'queued', runner_id = NULL, started_at = NULL,
				    attempt = attempt + 1, reserved_runner_id = NULL
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
// shared/exclusive job in an org — i.e. work that could run right now if a
// matching runner existed. Isolated jobs are intentionally excluded: their
// pool-aware reservation flow is exposed by QueuedIsolatedDemand and must not
// be turned into generic autoscaler demand.
//
// Matrix legs are clamped to their group's remaining max-parallel headroom.
// Mirroring AcquireJob's admission PREDICATE alone would not be enough: with 20
// queued legs of a `max-parallel: 2` group and none running, the running-count
// is 0 for all 20 rows, so all 20 pass and assignDemand counts 20 machines'
// worth of demand for work that can only ever run 2 at a time. Ranking the legs
// within their group and keeping only the first (max_parallel - running) is
// what makes the autoscaler and the dispatcher agree.
func (s *Store) QueuedDemand(ctx context.Context, orgID uuid.UUID) ([]QueuedJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.resource_tier, t.runs_on FROM (
		  SELECT j.resource_tier, j.runs_on, j.max_parallel,
		         row_number() OVER (PARTITION BY j.run_id, j.job_key
		                            ORDER BY j.ordinal ASC, j.name ASC) AS rn,
		         (SELECT count(*) FROM pipeline_jobs s
		          WHERE s.run_id = j.run_id AND s.job_key = j.job_key
		            AND s.status = 'running') AS running
		  FROM pipeline_jobs j
		  WHERE j.org_id = $1 AND j.status = 'queued'
		    AND j.execution_mode <> 'isolated'
		    AND `+dispatchableNeedsSQL+`
		) t
		WHERE t.max_parallel = 0
		   OR t.rn <= GREATEST(t.max_parallel - t.running, 0)
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
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// IsolatedJobDemand is one dispatchable isolated job awaiting a runner
// reservation. A reconcile loop can use Pool to launch or select capacity,
// then atomically bind that capacity with ReserveIsolatedJob.
type IsolatedJobDemand struct {
	JobID  uuid.UUID
	Pool   string
	Tier   string
	RunsOn []string
}

// QueuedIsolatedDemand lists dependency-satisfied isolated jobs that have no
// reservation yet. Like QueuedDemand, matrix legs are limited to their
// currently available max-parallel slots; no provider action happens here.
func (s *Store) QueuedIsolatedDemand(ctx context.Context, orgID uuid.UUID) ([]IsolatedJobDemand, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.execution_pool, t.resource_tier, t.runs_on
		FROM (
		  SELECT j.id, j.execution_pool, j.resource_tier, j.runs_on,
		         j.queued_at, j.ordinal, j.name, j.max_parallel,
		         row_number() OVER (PARTITION BY j.run_id, j.job_key
		                            ORDER BY j.ordinal ASC, j.name ASC) AS rn,
		         (SELECT count(*) FROM pipeline_jobs s
		          WHERE s.run_id = j.run_id AND s.job_key = j.job_key
		            AND (
		              s.status = 'running'
		              OR (s.status = 'queued' AND s.reserved_runner_id IS NOT NULL)
		            )) AS occupied
		  FROM pipeline_jobs j
		  WHERE j.org_id = $1
		    AND j.status = 'queued'
		    AND j.execution_mode = 'isolated'
		    AND j.reserved_runner_id IS NULL
		    AND `+dispatchableNeedsSQL+`
		) t
		WHERE t.max_parallel = 0
		   OR t.rn <= GREATEST(t.max_parallel - t.occupied, 0)
		ORDER BY t.queued_at ASC, t.ordinal ASC, t.name ASC
	`, orgID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()

	out := make([]IsolatedJobDemand, 0)
	for rows.Next() {
		var demand IsolatedJobDemand
		if err := rows.Scan(&demand.JobID, &demand.Pool, &demand.Tier, &demand.RunsOn); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, demand)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// ReserveIsolatedJob atomically assigns an unreserved queued isolated job to a
// runner from its declared pool. False means the job is no longer reservable
// (already reserved/claimed/finished, or the runner belongs to another pool).
func (s *Store) ReserveIsolatedJob(ctx context.Context, jobID, runnerID uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE pipeline_jobs j
		SET reserved_runner_id = $2
		FROM runners r
		WHERE j.id = $1
		  AND j.status = 'queued'
		  AND j.execution_mode = 'isolated'
		  AND j.reserved_runner_id IS NULL
		  AND r.id = $2
		  AND r.org_id = j.org_id
		  AND r.pool_name = j.execution_pool
		  AND r.isolated_job_id = j.id
	`, jobID, runnerID)
	if err != nil {
		return false, apperr.Internal(err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseIsolatedReservation clears a still-queued job's reservation after a
// failed provisioning attempt. It never unassigns a job that has already been
// claimed by the intended runner.
func (s *Store) ReleaseIsolatedReservation(ctx context.Context, jobID, runnerID uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE pipeline_jobs
		SET reserved_runner_id = NULL
		WHERE id = $1
		  AND status = 'queued'
		  AND execution_mode = 'isolated'
		  AND reserved_runner_id = $2
	`, jobID, runnerID)
	if err != nil {
		return false, apperr.Internal(err)
	}
	return tag.RowsAffected() == 1, nil
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
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}
