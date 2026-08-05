package runnercheck

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
)

const testRunnerConfig = `version: 1
tiers:
  small: { cpu: 1 }
pools:
  - name: aws-ready
    provider: aws
    tier: small
    os: linux
    aws:
      region: us-west-2
      ami: ami-1234
      instance_type: t3.small
      subnet_id: subnet-1234
      security_group_ids: [sg-1234]
      credentials_secret: AWS_CREDENTIALS
  - name: proxmox-placeholder
    provider: proxmox
    tier: small
    proxmox:
      credentials_secret: PROXMOX_CREDENTIALS
`

type fakeConfigReader struct {
	file *orgconfig.File
	err  error
}

func (f fakeConfigReader) Read(_ context.Context, _ uuid.UUID, _ string) (*orgconfig.File, error) {
	return f.file, f.err
}

type fakeSecretLister struct {
	secrets []model.Secret
	err     error
}

func (f fakeSecretLister) ListOrg(_ context.Context, _ uuid.UUID) ([]model.Secret, error) {
	return f.secrets, f.err
}

func TestPreflightChecksOnlyMetadataAndNeverRunsProbe(t *testing.T) {
	history := NewMemoryHistory(5)
	service := NewService(
		fakeConfigReader{file: &orgconfig.File{BlobSHA: "configured", Content: []byte(testRunnerConfig)}},
		fakeSecretLister{secrets: []model.Secret{{Name: "AWS_CREDENTIALS"}, {Name: "PROXMOX_CREDENTIALS"}}},
		history,
	)
	now := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	orgID := uuid.New()
	actorID := uuid.New()

	result := service.Preflight(context.Background(), Request{
		OrgID:       orgID,
		OrgSlug:     "demo-org",
		RequestedBy: actorID,
	})

	assert.Equal(t, PhasePreflight, result.Phase)
	assert.Equal(t, StatePreflight, result.State)
	assert.Equal(t, StateNotRun, result.RunnerProbeState)
	assert.Equal(t, CheckPassed, result.ConfigCheck.Status)
	assert.Equal(t, "memory", result.Storage)
	assert.Len(t, result.Pools, 2)

	aws := result.Pools[0]
	assert.Equal(t, "aws-ready", aws.PoolName)
	assert.Equal(t, ReadinessReady, aws.Readiness)
	assert.Equal(t, StateNotRun, aws.RunnerProbeState)
	assert.Equal(t, CheckPassed, checkByName(t, aws.Checks, "provider_support").Status)
	assert.Equal(t, CheckPassed, checkByName(t, aws.Checks, "credential_secret").Status)
	assert.Equal(t, CheckPassed, checkByName(t, aws.Checks, "os_network_structure").Status)

	proxmox := result.Pools[1]
	assert.Equal(t, ReadinessBlocked, proxmox.Readiness)
	assert.Equal(t, CheckUnsupported, checkByName(t, proxmox.Checks, "provider_support").Status)
	assert.Equal(t, CheckNotRun, checkByName(t, proxmox.Checks, "os_network_structure").Status)

	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "AWS_CREDENTIALS")
	assert.NotContains(t, string(encoded), "PROXMOX_CREDENTIALS")
	assert.Len(t, history.List(orgID), 1)
}

func TestPreflightReportsUnknownPoolAsBlockedWithoutCloudCall(t *testing.T) {
	service := NewService(
		fakeConfigReader{file: &orgconfig.File{BlobSHA: "configured", Content: []byte(testRunnerConfig)}},
		fakeSecretLister{secrets: []model.Secret{{Name: "AWS_CREDENTIALS"}}},
		NewMemoryHistory(5),
	)

	result := service.Preflight(context.Background(), Request{
		OrgID:     uuid.New(),
		OrgSlug:   "demo-org",
		PoolNames: []string{"does-not-exist", "does-not-exist"},
	})

	require.Len(t, result.Pools, 1)
	assert.Equal(t, "does-not-exist", result.Pools[0].PoolName)
	assert.Equal(t, ReadinessBlocked, result.Pools[0].Readiness)
	assert.Equal(t, StateNotRun, result.Pools[0].RunnerProbeState)
	assert.Equal(t, CheckFailed, checkByName(t, result.Pools[0].Checks, "pool_selection").Status)
}

func TestPreflightReportsInvalidConfigWithoutParserEcho(t *testing.T) {
	service := NewService(
		fakeConfigReader{file: &orgconfig.File{BlobSHA: "configured", Content: []byte("pools: [not valid")}},
		fakeSecretLister{err: errors.New("should not be called")},
		NewMemoryHistory(5),
	)

	result := service.Preflight(context.Background(), Request{OrgID: uuid.New(), OrgSlug: "demo-org"})

	assert.Equal(t, CheckFailed, result.ConfigCheck.Status)
	assert.Equal(t, StatePreflight, result.State)
	assert.Equal(t, StateNotRun, result.RunnerProbeState)
	assert.Empty(t, result.Pools)
	assert.NotContains(t, result.ConfigCheck.Message, "not valid")
}

func TestMemoryHistoryRetainsBoundedNewestFirstCopies(t *testing.T) {
	history := NewMemoryHistory(2)
	orgID := uuid.New()
	history.Append(orgID, Result{ID: "first", Pools: []PoolCheck{{PoolName: "one", Checks: []Check{{Name: "x"}}}}})
	history.Append(orgID, Result{ID: "second"})
	history.Append(orgID, Result{ID: "third"})

	results := history.List(orgID)
	require.Len(t, results, 2)
	assert.Equal(t, "third", results[0].ID)
	assert.Equal(t, "second", results[1].ID)

	results[0].ID = "mutated"
	assert.Equal(t, "third", history.List(orgID)[0].ID)
}

func checkByName(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	require.Failf(t, "check missing", "expected %q", name)
	return Check{}
}
