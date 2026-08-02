package pipelinestore_test

// Dispatch-path tests for strategy.matrix. These exercise the parts of
// AcquireJob / CompleteJob / CancelRun that are pure SQL and therefore cannot
// be covered by the parser's unit tests: the max-parallel admission gate, the
// fail-fast sibling cascade, and `needs` resolving against job_key rather than
// the (now suffixed) display name.
//
// dbtest.Open skips the whole package when Docker is unreachable, so this stays
// runnable for contributors without it while still gating CI, which has Docker.

import (
	"context"
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

// seedRepo inserts the org/project/repo chain a run needs to reference.
func seedRepo(t *testing.T, pool *db.Pool) (orgID, projectID, repoID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	orgID, projectID, repoID = uuid.New(), uuid.New(), uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO orgs (id, slug) VALUES ($1, $2)`, orgID, "org-"+orgID.String()[:8])
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, slug) VALUES ($1, $2, $3)`,
		projectID, orgID, "proj-"+projectID.String()[:8])
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO repos (id, project_id, slug) VALUES ($1, $2, $3)`,
		repoID, projectID, "repo-"+repoID.String()[:8])
	require.NoError(t, err)
	return orgID, projectID, repoID
}

func newRunner(t *testing.T, rs *runnerstore.Store, orgID uuid.UUID, labels []string) uuid.UUID {
	t.Helper()
	r, err := rs.CreateEphemeralRunner(context.Background(), orgID,
		"runner-"+uuid.New().String()[:8], labels, model.TierMedium, "aws", "pool", model.OSLinux)
	require.NoError(t, err)
	return r.ID
}

func createRun(t *testing.T, ps *pipelinestore.Store, orgID, projectID, repoID uuid.UUID, src string) *model.PipelineRun {
	t.Helper()
	wf, err := pipeline.Parse([]byte(src))
	require.NoError(t, err, "workflow must parse")
	run, err := ps.CreateRun(context.Background(), pipelinestore.CreateRunParams{
		OrgID: orgID, ProjectID: projectID, RepoID: repoID,
		WorkflowPath: ".wuling/workflows/ci.yml", Event: "push",
		GitRef: "refs/heads/main", CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		Workflow: wf, DefaultTier: model.TierMedium,
	})
	require.NoError(t, err)
	return run
}

func jobsByName(t *testing.T, ps *pipelinestore.Store, runID uuid.UUID) map[string]model.PipelineJob {
	t.Helper()
	run, err := ps.GetRunWithSteps(context.Background(), runID)
	require.NoError(t, err)
	out := make(map[string]model.PipelineJob, len(run.Jobs))
	for _, j := range run.Jobs {
		out[j.Name] = j
	}
	return out
}

const matrixWorkflow = `
name: CI
on: push
jobs:
  build:
    strategy:
      max-parallel: 2
      matrix:
        os: [a, b, c, d]
    runs-on: [linux]
    env:
      T: ${{ matrix.os }}
    steps:
      - run: make ${{ matrix.os }}
  test:
    needs: [build]
    runs-on: [linux]
    steps:
      - run: make test
`

// A matrix job expands to one row per combination, every leg sharing the YAML
// job id as job_key, with ${{ matrix.* }} already substituted into the
// persisted definition.
func TestCreateRunExpandsMatrix(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ps := pipelinestore.New(pool, t.TempDir())

	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, ps, orgID, projectID, repoID, matrixWorkflow)

	jobs := jobsByName(t, ps, run.ID)
	require.Len(t, jobs, 5, "4 matrix legs + 1 dependent job")
	for _, os := range []string{"a", "b", "c", "d"} {
		j, ok := jobs["build ("+os+")"]
		require.True(t, ok, "missing leg for os=%s; got %v", os, jobs)
		require.Equal(t, "build", j.JobKey, "every leg shares the YAML job id")
		require.Equal(t, map[string]string{"os": os}, j.Matrix)
	}
	require.Equal(t, "test", jobs["test"].JobKey)
}

// max-parallel caps how many legs of one job_key may be running at once. The
// third runner must come away empty even though two legs are still queued.
func TestAcquireHonoursMaxParallel(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ps := pipelinestore.New(pool, t.TempDir())
	rs := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	createRun(t, ps, orgID, projectID, repoID, matrixWorkflow)

	var acquired []string
	for i := 0; i < 3; i++ {
		aj, err := ps.AcquireJob(ctx, newRunner(t, rs, orgID, []string{"linux"}))
		require.NoError(t, err)
		if aj != nil {
			acquired = append(acquired, aj.JobName)
		}
	}
	require.Len(t, acquired, 2, "max-parallel: 2 must throttle the third runner, got %v", acquired)
}

// With fail-fast (the default), one failed leg cancels its queued siblings, and
// the dependent job — which needs the whole group — is cancelled too.
func TestFailFastCancelsSiblingsAndDependents(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ps := pipelinestore.New(pool, t.TempDir())
	rs := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, ps, orgID, projectID, repoID, matrixWorkflow)

	runnerID := newRunner(t, rs, orgID, []string{"linux"})
	aj, err := ps.AcquireJob(ctx, runnerID)
	require.NoError(t, err)
	require.NotNil(t, aj)
	require.NoError(t, ps.CompleteJob(ctx, aj.JobID, "failed"))

	jobs := jobsByName(t, ps, run.ID)
	var failed, canceled int
	for _, j := range jobs {
		switch j.Status {
		case "failed":
			failed++
		case "canceled":
			canceled++
		}
	}
	require.Equal(t, 1, failed)
	require.Equal(t, 4, canceled, "3 sibling legs + the dependent job: %v", jobs)

	got, err := ps.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", got.Status)

	// The runner that held the failed job must not be left 'busy', or the
	// autoscaler counts it as working forever and never scales it down.
	var busy int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM runners WHERE status = 'busy'`).Scan(&busy))
	require.Zero(t, busy)
}

// fail-fast: false lets the surviving legs run to completion.
func TestFailFastDisabledKeepsSiblings(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ps := pipelinestore.New(pool, t.TempDir())
	rs := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	createRun(t, ps, orgID, projectID, repoID, `
name: CI
on: push
jobs:
  build:
    strategy:
      fail-fast: false
      matrix:
        os: [a, b]
    runs-on: [linux]
    steps:
      - run: make ${{ matrix.os }}
`)

	aj, err := ps.AcquireJob(ctx, newRunner(t, rs, orgID, []string{"linux"}))
	require.NoError(t, err)
	require.NotNil(t, aj)
	require.NoError(t, ps.CompleteJob(ctx, aj.JobID, "failed"))

	// The sibling is still dispatchable rather than cancelled.
	next, err := ps.AcquireJob(ctx, newRunner(t, rs, orgID, []string{"linux"}))
	require.NoError(t, err)
	require.NotNil(t, next, "fail-fast:false must leave the sibling queued")
}

// `needs` resolves against job_key, so a dependent job waits for EVERY leg of
// the group — not just the first one to finish.
func TestNeedsWaitsForEveryLeg(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ps := pipelinestore.New(pool, t.TempDir())
	rs := runnerstore.New(pool)
	ctx := context.Background()

	orgID, projectID, repoID := seedRepo(t, pool)
	createRun(t, ps, orgID, projectID, repoID, `
name: CI
on: push
jobs:
  build:
    strategy:
      matrix:
        os: [a, b]
    runs-on: [linux]
    steps:
      - run: make ${{ matrix.os }}
  test:
    needs: [build]
    runs-on: [linux]
    steps:
      - run: make test
`)

	// Finish the first leg; `test` must still be unreachable.
	first, err := ps.AcquireJob(ctx, newRunner(t, rs, orgID, []string{"linux"}))
	require.NoError(t, err)
	require.NoError(t, ps.CompleteJob(ctx, first.JobID, "success"))

	second, err := ps.AcquireJob(ctx, newRunner(t, rs, orgID, []string{"linux"}))
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, "build", second.JobKey, "the other leg must come before test, got %q", second.JobName)

	require.NoError(t, ps.CompleteJob(ctx, second.JobID, "success"))
	third, err := ps.AcquireJob(ctx, newRunner(t, rs, orgID, []string{"linux"}))
	require.NoError(t, err)
	require.NotNil(t, third)
	require.Equal(t, "test", third.JobKey, "test unlocks only after every leg succeeds")
}
