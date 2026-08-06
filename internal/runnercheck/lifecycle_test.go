package runnercheck

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/secretbox"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
)

const lifecycleRunnerConfig = `version: 1
tiers:
  medium: { cpu: 2 }
pools:
  - name: aws-linux
    provider: aws
    tier: medium
    os: linux
    labels: [linux]
    aws:
      region: us-west-2
      ami: ami-1234
      instance_type: t3.medium
      subnet_id: subnet-1234
      security_group_ids: [sg-1234]
      credentials_secret: AWS_CREDENTIALS
`

type lifecycleConfigReader struct {
	file    *orgconfig.File
	project *model.Project
	repo    *model.Repo
}

func (r lifecycleConfigReader) Read(_ context.Context, _ uuid.UUID, _ string) (*orgconfig.File, error) {
	return r.file, nil
}

func (r lifecycleConfigReader) Locate(_ context.Context, _ uuid.UUID) (*model.Project, *model.Repo, string, error) {
	return r.project, r.repo, "", nil
}

func TestStartQueuesDurableExplicitPoolProbe(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ctx := context.Background()
	orgID, userID, project, repo := seedLifecycleRepo(t, pool)
	audits := newLifecycleAuditStore(t, pool)
	pipelines := pipelinestore.New(pool, t.TempDir())
	service := NewLifecycleService(
		lifecycleConfigReader{
			file:    &orgconfig.File{BlobSHA: "configured", CommitSHA: "abc123", Content: []byte(lifecycleRunnerConfig)},
			project: project,
			repo:    repo,
		},
		fakeSecretLister{secrets: []model.Secret{{Name: "AWS_CREDENTIALS"}}},
		audits,
		pipelines,
		true,
	)

	result, err := service.Start(ctx, Request{
		OrgID: orgID, OrgSlug: "self-check-org", RequestedBy: userID, PoolNames: []string{"aws-linux"},
	})
	require.NoError(t, err)
	require.Equal(t, CheckPassed, result.ConfigCheck.Status)
	require.Len(t, result.Checks, 1)
	require.Empty(t, result.BlockedPools)
	record := result.Checks[0]
	require.Equal(t, StateQueued, record.State)
	require.NotNil(t, record.RunID)
	require.NotNil(t, record.JobID)
	assert.Empty(t, record.ExternalID)

	status, err := pipelines.GetIsolatedJobStatus(ctx, *record.JobID)
	require.NoError(t, err)
	assert.Equal(t, "aws-linux", status.Pool)
	assert.Equal(t, "queued", status.Status)

	secret, err := audits.ProbeSecret(ctx, record.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, secret)
	assert.NotContains(t, record.Summary, secret)

	_, err = service.Start(ctx, Request{
		OrgID: orgID, OrgSlug: "self-check-org", RequestedBy: userID, PoolNames: []string{"aws-linux"},
	})
	require.Error(t, err)
	require.Equal(t, apperr.CodeConflict, apperr.As(err).Code)
}

func TestCompleteProbeVerifiesHMACAndRedactionCanary(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ctx := context.Background()
	orgID, userID, project, repo := seedLifecycleRepo(t, pool)
	audits := newLifecycleAuditStore(t, pool)
	pipelines := pipelinestore.New(pool, t.TempDir())
	service := NewLifecycleService(nil, nil, audits, pipelines, true)

	workflow, err := pipeline.Parse([]byte(`
name: probe
on: workflow_dispatch
jobs:
  probe:
    runs-on: [linux]
    execution: {mode: isolated, pool: aws-linux}
    steps:
      - run: echo probe
`))
	require.NoError(t, err)
	run, err := pipelines.CreateRun(ctx, pipelinestore.CreateRunParams{
		OrgID: orgID, ProjectID: project.ID, RepoID: repo.ID,
		WorkflowPath: ".wuling/internal/runner-self-check.yml",
		Event:        "manual", GitRef: "main", CommitSHA: "abc123", TriggeredBy: userID,
		Workflow: workflow, DefaultTier: model.TierMedium,
	})
	require.NoError(t, err)
	require.Len(t, run.Jobs, 1)
	jobID := run.Jobs[0].ID
	audit, err := audits.Create(ctx, CreateAuditParams{
		OrgID: orgID, RequestedBy: userID, PoolName: "aws-linux", Provider: "aws", OS: model.OSLinux,
		Checks: []Check{
			{Name: "config_parse", Status: CheckPassed},
			{Name: "runner_data_disk", Status: CheckPassed},
		},
		ProbeSecret: "a-probe-secret-that-must-not-leak",
	})
	require.NoError(t, err)
	require.NoError(t, audits.LinkPipeline(ctx, audit.ID, run.ID, jobID))

	runners := runnerstore.New(pool)
	runner, err := runners.CreateIsolatedEphemeralRunner(
		ctx, orgID, "probe-runner", []string{"linux"}, model.TierMedium, "aws", "aws-linux", model.OSLinux, jobID,
	)
	require.NoError(t, err)
	reserved, err := pipelines.ReserveIsolatedJob(ctx, jobID, runner.ID)
	require.NoError(t, err)
	require.True(t, reserved)
	acquired, err := pipelines.AcquireJob(ctx, runner.ID)
	require.NoError(t, err)
	require.NotNil(t, acquired)

	secret, err := audits.ProbeSecret(ctx, audit.ID)
	require.NoError(t, err)
	log := probeDataDiskMarker + "\n" +
		"WULING_SELF_CHECK_COMMAND=ok\n" +
		"WULING_SELF_CHECK_PROOF=" + probeProof(secret) + "\n" +
		"WULING_SELF_CHECK_SECRET_ECHO=[REDACTED]\n"
	_, err = pipelines.AppendLog(ctx, jobID, []byte(log))
	require.NoError(t, err)
	require.NoError(t, pipelines.CompleteJob(ctx, jobID, "success"))
	service.CompleteProbe(ctx, jobID, "success")

	records, err := audits.List(ctx, orgID, 10)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, StateSucceeded, records[0].State)
	assert.Contains(t, records[0].Summary, "通过")
	assert.Contains(t, string(records[0].Checks), "stream_log_redaction")
	assert.Contains(t, string(records[0].Checks), "runner_data_disk_boot")
	assert.NotContains(t, string(records[0].Checks), secret)
}

func TestProvisioningRetryRetainsProbeSecretUntilFinalCleanup(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ctx := context.Background()
	orgID, userID, project, repo := seedLifecycleRepo(t, pool)
	audits := newLifecycleAuditStore(t, pool)
	pipelines := pipelinestore.New(pool, t.TempDir())

	workflow, err := pipeline.Parse([]byte(`
name: retry probe
on: workflow_dispatch
jobs:
  probe:
    execution: {mode: isolated, pool: aws-linux}
    steps: [{run: echo probe}]
`))
	require.NoError(t, err)
	run, err := pipelines.CreateRun(ctx, pipelinestore.CreateRunParams{
		OrgID: orgID, ProjectID: project.ID, RepoID: repo.ID,
		WorkflowPath: ".wuling/internal/runner-self-check.yml",
		Event:        "manual", GitRef: "main", CommitSHA: "abc123", TriggeredBy: userID,
		Workflow: workflow, DefaultTier: model.TierMedium,
	})
	require.NoError(t, err)
	audit, err := audits.Create(ctx, CreateAuditParams{
		OrgID: orgID, RequestedBy: userID, PoolName: "aws-linux", Provider: "aws", OS: model.OSLinux,
		Checks: []Check{{Name: "config_parse", Status: CheckPassed}}, ProbeSecret: "retry-probe-secret",
	})
	require.NoError(t, err)
	require.NoError(t, audits.LinkPipeline(ctx, audit.ID, run.ID, run.Jobs[0].ID))

	require.NoError(t, audits.MarkIsolatedProvisioningFailure(ctx, run.Jobs[0].ID, "runner did not acquire before timeout"))
	secret, err := audits.ProbeSecret(ctx, audit.ID)
	require.NoError(t, err)
	require.Equal(t, "retry-probe-secret", secret)

	require.NoError(t, audits.MarkIsolatedCleaned(ctx, run.Jobs[0].ID))
	_, err = audits.ProbeSecret(ctx, audit.ID)
	require.Error(t, err)
	apiErr := apperr.As(err)
	require.NotNil(t, apiErr)
	require.Equal(t, apperr.CodeNotFound, apiErr.Code)
}

func TestProbeScriptsUseOneTimeValueOnlyForHMACAndCanary(t *testing.T) {
	linux := probeScript(model.OSLinux)
	assert.Contains(t, linux, "hmac.new")
	assert.Contains(t, linux, probeSecretEnv)
	assert.Contains(t, linux, "WULING_SELF_CHECK_REQUIRE_DATA_DISK")
	assert.Contains(t, linux, probeDataDiskMarker)
	assert.Contains(t, linux, "WULING_SELF_CHECK_SECRET_ECHO=")

	windows := probeScript(model.OSWindows)
	assert.Contains(t, windows, "HMACSHA256")
	assert.Contains(t, windows, probeSecretEnv)
	assert.Contains(t, windows, "WULING_SELF_CHECK_REQUIRE_DATA_DISK")
	assert.Contains(t, windows, probeDataDiskMarker)
	assert.Contains(t, windows, "WULING_SELF_CHECK_SECRET_ECHO=")

	assert.NotEqual(t, probeProof("one"), probeProof("two"))
}

func newLifecycleAuditStore(t *testing.T, pool *db.Pool) *AuditStore {
	t.Helper()
	box, err := secretbox.New(secretbox.GenerateKey())
	require.NoError(t, err)
	return NewAuditStore(pool, box)
}

func seedLifecycleRepo(t *testing.T, pool *db.Pool) (uuid.UUID, uuid.UUID, *model.Project, *model.Repo) {
	t.Helper()
	ctx := context.Background()
	orgID, userID, projectID, repoID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, email) VALUES ($1, $2, $3)
	`, userID, "self-check-"+userID.String()[:8], userID.String()[:8]+"@example.test")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO orgs (id, slug) VALUES ($1, $2)`, orgID, "self-check-org")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO projects (id, org_id, slug) VALUES ($1, $2, 'config')
	`, projectID, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO repos (id, project_id, slug, default_branch) VALUES ($1, $2, 'config', 'main')
	`, repoID, projectID)
	require.NoError(t, err)
	return orgID, userID,
		&model.Project{ID: projectID, OrgID: orgID, Slug: "config"},
		&model.Repo{ID: repoID, ProjectID: projectID, Slug: "config", DefaultBranch: "main"}
}
