package pipelinestore_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
)

func newRunnerInPool(t *testing.T, rs *runnerstore.Store, orgID uuid.UUID, pool string) uuid.UUID {
	t.Helper()
	runner, err := rs.CreateEphemeralRunner(
		context.Background(),
		orgID,
		"runner-"+uuid.New().String()[:8],
		[]string{"linux"},
		model.TierMedium,
		"aws",
		pool,
		model.OSLinux,
	)
	require.NoError(t, err)
	return runner.ID
}

func runnerStatus(t *testing.T, pool *db.Pool, runnerID uuid.UUID) string {
	t.Helper()
	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM runners WHERE id = $1`, runnerID).Scan(&status))
	return status
}

func TestCreateRunPersistsExecutionModes(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())

	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, store, orgID, projectID, repoID, `
name: execution modes
on: push
jobs:
  shared:
    steps: [{run: echo shared}]
  exclusive:
    execution: {mode: exclusive}
    steps: [{run: echo exclusive}]
  isolated:
    execution: {mode: isolated, pool: dedicated-linux}
    steps: [{run: echo isolated}]
`)

	type persisted struct {
		mode string
		pool string
		spec pipeline.JobSpec
	}
	got := map[string]persisted{}
	rows, err := pool.Query(context.Background(), `
		SELECT name, execution_mode, execution_pool, definition
		FROM pipeline_jobs
		WHERE run_id = $1
	`, run.ID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var name string
		var value persisted
		var raw []byte
		require.NoError(t, rows.Scan(&name, &value.mode, &value.pool, &raw))
		require.NoError(t, json.Unmarshal(raw, &value.spec))
		got[name] = value
	}
	require.NoError(t, rows.Err())

	require.Equal(t, pipeline.ExecutionModeShared, got["shared"].mode)
	require.Empty(t, got["shared"].pool)
	require.Equal(t, pipeline.Execution{Mode: pipeline.ExecutionModeShared}, got["shared"].spec.Execution)
	require.Equal(t, pipeline.ExecutionModeExclusive, got["exclusive"].mode)
	require.Empty(t, got["exclusive"].pool)
	require.Equal(t, pipeline.Execution{Mode: pipeline.ExecutionModeExclusive}, got["exclusive"].spec.Execution)
	require.Equal(t, pipeline.ExecutionModeIsolated, got["isolated"].mode)
	require.Equal(t, "dedicated-linux", got["isolated"].pool)
	require.Equal(t, pipeline.Execution{Mode: pipeline.ExecutionModeIsolated, Pool: "dedicated-linux"}, got["isolated"].spec.Execution)

	// Existing run-detail scans intentionally remain valid after the new
	// denormalized columns are present.
	detail, err := store.GetRunWithSteps(context.Background(), run.ID)
	require.NoError(t, err)
	require.Len(t, detail.Jobs, 3)
}

func TestExclusiveAcquisitionSerializesOneRunner(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	runners := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	createRun(t, store, orgID, projectID, repoID, `
name: exclusive
on: push
jobs:
  a:
    execution: {mode: exclusive}
    steps: [{run: echo a}]
  b:
    execution: {mode: exclusive}
    steps: [{run: echo b}]
`)
	runnerID := newRunner(t, runners, orgID, []string{"linux"})

	type result struct {
		job *pipelinestore.AcquiredJob
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			job, err := store.AcquireJob(ctx, runnerID)
			results <- result{job: job, err: err}
		}()
	}
	close(start)

	acquired := 0
	for i := 0; i < 2; i++ {
		result := <-results
		require.NoError(t, result.err)
		if result.job != nil {
			acquired++
			require.Equal(t, pipeline.ExecutionModeExclusive, result.job.Spec.Execution.Mode)
		}
	}
	require.Equal(t, 1, acquired, "two workers must not claim exclusive jobs on one runner")

	var running int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM pipeline_jobs
		WHERE runner_id = $1 AND status = 'running'
	`, runnerID).Scan(&running))
	require.Equal(t, 1, running)

	// An already-running exclusive job also blocks a later shared claim on the
	// same runner, preserving exclusivity for its entire execution.
	createRun(t, store, orgID, projectID, repoID, `
name: later shared
on: push
jobs:
  shared:
    steps: [{run: echo shared}]
`)
	blocked, err := store.AcquireJob(ctx, runnerID)
	require.NoError(t, err)
	require.Nil(t, blocked)
}

func TestCompleteKeepsRunnerBusyWhileSharedJobRemains(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	runners := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	createRun(t, store, orgID, projectID, repoID, `
name: shared
on: push
jobs:
  a:
    steps: [{run: echo a}]
  b:
    steps: [{run: echo b}]
`)
	runnerID := newRunner(t, runners, orgID, []string{"linux"})
	first, err := store.AcquireJob(ctx, runnerID)
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := store.AcquireJob(ctx, runnerID)
	require.NoError(t, err)
	require.NotNil(t, second)

	require.NoError(t, store.CompleteJob(ctx, first.JobID, "success"))
	require.Equal(t, "busy", runnerStatus(t, pool, runnerID))
	require.NoError(t, store.CompleteJob(ctx, second.JobID, "success"))
	require.Equal(t, "idle", runnerStatus(t, pool, runnerID))
}

func TestCancelRunKeepsRunnerBusyForAnotherRun(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	runners := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	firstRun := createRun(t, store, orgID, projectID, repoID, `
name: first
on: push
jobs:
  first:
    steps: [{run: echo first}]
`)
	runnerID := newRunner(t, runners, orgID, []string{"linux"})
	first, err := store.AcquireJob(ctx, runnerID)
	require.NoError(t, err)
	require.NotNil(t, first)

	createRun(t, store, orgID, projectID, repoID, `
name: second
on: push
jobs:
  second:
    steps: [{run: echo second}]
`)
	second, err := store.AcquireJob(ctx, runnerID)
	require.NoError(t, err)
	require.NotNil(t, second)

	require.NoError(t, store.CancelRun(ctx, firstRun.ID))
	require.Equal(t, "busy", runnerStatus(t, pool, runnerID))
	require.NoError(t, store.CompleteJob(ctx, second.JobID, "success"))
	require.Equal(t, "idle", runnerStatus(t, pool, runnerID))
}

func TestIsolatedJobRequiresMatchingReservation(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	runners := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, store, orgID, projectID, repoID, `
name: isolated
on: push
jobs:
  build:
    runs-on: [linux]
    execution: {mode: isolated, pool: dedicated}
    steps: [{run: make}]
`)
	jobs := jobsByName(t, store, run.ID)
	jobID := jobs["build"].ID

	otherPool := newRunnerInPool(t, runners, orgID, "other")
	job, err := store.AcquireJob(ctx, otherPool)
	require.NoError(t, err)
	require.Nil(t, job, "an unreserved isolated job must not be claimed")

	generic, err := store.QueuedDemand(ctx, orgID)
	require.NoError(t, err)
	require.Empty(t, generic, "isolated work must not trigger generic demand")
	demand, err := store.QueuedIsolatedDemand(ctx, orgID)
	require.NoError(t, err)
	require.Len(t, demand, 1)
	require.Equal(t, jobID, demand[0].JobID)
	require.Equal(t, "dedicated", demand[0].Pool)

	reserved, err := store.ReserveIsolatedJob(ctx, jobID, otherPool)
	require.NoError(t, err)
	require.False(t, reserved, "a runner from another pool cannot receive the reservation")

	genericDedicatedRunner := newRunnerInPool(t, runners, orgID, "dedicated")
	reserved, err = store.ReserveIsolatedJob(ctx, jobID, genericDedicatedRunner)
	require.NoError(t, err)
	require.False(t, reserved, "a reusable runner must never reserve an isolated job")

	isolatedRunner, err := runners.CreateIsolatedEphemeralRunner(
		ctx,
		orgID,
		"isolated-"+uuid.New().String()[:8],
		[]string{"linux"},
		model.TierMedium,
		"aws",
		"dedicated",
		model.OSLinux,
		jobID,
	)
	require.NoError(t, err)
	reservedRunner := isolatedRunner.ID
	reserved, err = store.ReserveIsolatedJob(ctx, jobID, reservedRunner)
	require.NoError(t, err)
	require.True(t, reserved)

	anotherDedicatedRunner := newRunnerInPool(t, runners, orgID, "dedicated")
	job, err = store.AcquireJob(ctx, anotherDedicatedRunner)
	require.NoError(t, err)
	require.Nil(t, job, "only the reserved runner may claim an isolated job")

	job, err = store.AcquireJob(ctx, reservedRunner)
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, jobID, job.JobID)
	require.Equal(t, pipeline.Execution{Mode: pipeline.ExecutionModeIsolated, Pool: "dedicated"}, job.Spec.Execution)
}

func TestIsolatedReservationCanRetryAfterTimedOutRunnerIsRemoved(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	runners := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, store, orgID, projectID, repoID, `
name: isolated retry
on: push
jobs:
  build:
    runs-on: [linux]
    execution: {mode: isolated, pool: dedicated}
    steps: [{run: make}]
`)
	jobID := jobsByName(t, store, run.ID)["build"].ID

	first, err := runners.CreateIsolatedEphemeralRunner(
		ctx, orgID, "first-isolated", []string{"linux"}, model.TierMedium,
		"aws", "dedicated", model.OSLinux, jobID,
	)
	require.NoError(t, err)
	reserved, err := store.ReserveIsolatedJob(ctx, jobID, first.ID)
	require.NoError(t, err)
	require.True(t, reserved)
	released, err := store.ReleaseIsolatedReservation(ctx, jobID, first.ID)
	require.NoError(t, err)
	require.True(t, released)
	require.NoError(t, runners.Delete(ctx, orgID, first.ID))

	second, err := runners.CreateIsolatedEphemeralRunner(
		ctx, orgID, "second-isolated", []string{"linux"}, model.TierMedium,
		"aws", "dedicated", model.OSLinux, jobID,
	)
	require.NoError(t, err)
	reserved, err = store.ReserveIsolatedJob(ctx, jobID, second.ID)
	require.NoError(t, err)
	require.True(t, reserved, "the timed-out job should be reservable by a fresh VM")
}
