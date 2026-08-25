package githubwebhook_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/githubapp"
	"github.com/zixiao-labs/wuling-devops/internal/githubwebhook"
	"github.com/zixiao-labs/wuling-devops/internal/notification"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

func TestNormalizeCheckColor(t *testing.T) {
	assert.Equal(t, githubwebhook.CheckColorGreen, githubwebhook.NormalizeCheckColor("success"))
	for _, conclusion := range []string{"failure", "cancelled", "timed_out", "neutral", "skipped", ""} {
		t.Run(conclusion, func(t *testing.T) {
			assert.Equal(t, githubwebhook.CheckColorRed, githubwebhook.NormalizeCheckColor(conclusion))
		})
	}
}

func TestProcessor_ExternalCheckRunCompletionBecomesGreenAndEnqueuesNotification(t *testing.T) {
	proc, checks, pool, repoID := linkedCheckProcessor(t)
	proc.App = &fakeAppClient{createID: 8001}
	body := []byte(`{
		"action":"completed",
		"check_run":{
			"id":987654,
			"name":"build (linux)",
			"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"status":"completed",
			"conclusion":"success",
			"details_url":"https://github.com/acme/app/runs/987654",
			"completed_at":"2026-08-24T09:30:00Z",
			"app":{"id":15368,"slug":"github-actions","name":"GitHub Actions"}
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)
	event := githubwebhook.EventContext{
		DeliveryID: "external-check-green",
		Event:      "check_run",
		Body:       body,
		Log:        discardLogger(),
	}

	require.NoError(t, proc.Handle(event))
	// Processor-level replay proves the notification boundary is independently
	// idempotent in addition to the HTTP delivery ledger.
	require.NoError(t, proc.Handle(event))

	got, err := checks.Get(context.Background(), repoID, "check_run", "987654")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "build (linux)", got.Name)
	assert.Equal(t, "github-actions", got.Provider)
	assert.Equal(t, "success", got.Conclusion)
	assert.Equal(t, githubwebhook.CheckColorGreen, got.Color)

	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM notification_outbox
		WHERE event_type = 'github.check.completed' AND repo_id = $1
	`, repoID).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestProcessor_WorkflowRunCompletionTracksAttemptAndFailure(t *testing.T) {
	proc, checks, pool, repoID := linkedCheckProcessor(t)
	proc.App = &fakeAppClient{createID: 8002}
	body := []byte(`{
		"action":"completed",
		"workflow_run":{
			"id":456789,
			"name":"CI",
			"display_title":"CI for main",
			"head_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"status":"completed",
			"conclusion":"failure",
			"html_url":"https://github.com/acme/app/actions/runs/456789",
			"run_attempt":2,
			"updated_at":"2026-08-24T10:00:00Z"
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)

	require.NoError(t, proc.Handle(githubwebhook.EventContext{
		DeliveryID: "workflow-run-red",
		Event:      "workflow_run",
		Body:       body,
		Log:        discardLogger(),
	}))

	got, err := checks.Get(context.Background(), repoID, "workflow_run", "456789")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "github-actions", got.Provider)
	assert.Equal(t, 2, got.Attempt)
	assert.Equal(t, "failure", got.Conclusion)
	assert.Equal(t, githubwebhook.CheckColorRed, got.Color)

	var color string
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT payload->>'color' FROM notification_outbox
		WHERE event_type = 'github.check.completed' AND repo_id = $1
	`, repoID).Scan(&color))
	assert.Equal(t, githubwebhook.CheckColorRed, color)
}

func TestProcessor_WorkflowRunCompletionEchoesStableRequiredCheck(t *testing.T) {
	proc, checks, _, repoID := linkedCheckProcessor(t)
	app := &fakeAppClient{createID: 9001}
	proc.App = app
	body := []byte(`{
		"action":"completed",
		"workflow_run":{
			"id":456789,
			"name":"CI",
			"head_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"status":"completed",
			"conclusion":"failure",
			"html_url":"https://github.com/acme/app/actions/runs/456789",
			"run_attempt":2,
			"updated_at":"2026-08-24T10:00:00Z"
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)
	event := githubwebhook.EventContext{
		DeliveryID: "workflow-run-feedback",
		Event:      "workflow_run",
		Body:       body,
		Log:        discardLogger(),
	}

	require.NoError(t, proc.Handle(event))
	require.Len(t, app.creates, 1)
	created := app.creates[0]
	assert.Equal(t, int64(42), app.installationIDs[0])
	assert.Equal(t, "installation-token", created.token)
	assert.Equal(t, "acme", created.owner)
	assert.Equal(t, "app", created.repo)
	assert.Equal(t, "武陵监听 / GitHub Actions / CI", created.body.Name)
	assert.Equal(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", created.body.HeadSHA)
	assert.Equal(t, "completed", created.body.Status)
	assert.Equal(t, "failure", created.body.Conclusion)
	assert.Equal(t, "2026-08-24T10:00:00Z", created.body.CompletedAt)
	assert.Equal(t, "https://github.com/acme/app/actions/runs/456789", created.body.DetailsURL)
	assert.Equal(t, "wuling-monitor:workflow_run:456789", created.body.ExternalID)
	require.NotNil(t, created.body.Output)
	assert.Equal(t, created.body.Name, created.body.Output.Title)

	stored, err := checks.Get(context.Background(), repoID, "workflow_run", "456789")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, int64(9001), stored.FeedbackCheckRunID)

	// Processor-level replay reuses the persisted feedback ID. GitHub delivery
	// de-duplication happens one layer above and is covered separately.
	require.NoError(t, proc.Handle(event))
	require.Len(t, app.creates, 1)
	require.Len(t, app.updates, 1)
	assert.Equal(t, int64(9001), app.updates[0].checkRunID)
	assert.Equal(t, created.body.Name, app.updates[0].body.Name)
	assert.Equal(t, created.body.ExternalID, app.updates[0].body.ExternalID)
	assert.Equal(t, created.body.CompletedAt, app.updates[0].body.CompletedAt)
}

func TestProcessor_ThirdPartyCheckCompletionEchoesProviderContext(t *testing.T) {
	proc, checks, _, repoID := linkedCheckProcessor(t)
	app := &fakeAppClient{createID: 9002}
	proc.App = app
	body := []byte(`{
		"action":"completed",
		"check_run":{
			"id":7654321,
			"name":"codecov/project",
			"head_sha":"cccccccccccccccccccccccccccccccccccccccc",
			"status":"completed",
			"conclusion":"success",
			"details_url":"https://app.codecov.io/gh/acme/app/commit/cccc",
			"completed_at":"2026-08-24T11:00:00Z",
			"app":{"id":254,"slug":"codecov","name":"Codecov"}
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)

	require.NoError(t, proc.Handle(githubwebhook.EventContext{
		DeliveryID: "third-party-feedback",
		Event:      "check_run",
		Body:       body,
		Log:        discardLogger(),
	}))
	require.Len(t, app.creates, 1)
	created := app.creates[0]
	assert.Equal(t, "武陵监听 / codecov / codecov/project", created.body.Name)
	assert.Equal(t, "success", created.body.Conclusion)
	assert.Equal(t, "wuling-monitor:check_run:7654321", created.body.ExternalID)

	stored, err := checks.Get(context.Background(), repoID, "check_run", "7654321")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, int64(9002), stored.FeedbackCheckRunID)
}

func TestProcessor_CheckCompletionWithoutAppFailsInsteadOfReturningSuccess(t *testing.T) {
	proc, checks, _, repoID := linkedCheckProcessor(t)
	body := []byte(`{
		"action":"completed",
		"check_run":{
			"id":7654322,
			"name":"build",
			"head_sha":"cccccccccccccccccccccccccccccccccccccccc",
			"status":"completed",
			"conclusion":"success",
			"completed_at":"2026-08-24T11:00:00Z",
			"app":{"id":15368,"slug":"github-actions","name":"GitHub Actions"}
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)
	event := githubwebhook.EventContext{
		DeliveryID: "missing-app-feedback",
		Event:      "check_run",
		Body:       body,
		Log:        discardLogger(),
	}

	err := proc.Handle(event)
	require.ErrorContains(t, err, "github app client is not configured")

	// Observation remains durable, while a GitHub redelivery can retry only
	// the missing feedback after the operator fixes the App credentials.
	stored, getErr := checks.Get(context.Background(), repoID, "check_run", "7654322")
	require.NoError(t, getErr)
	require.NotNil(t, stored)
	assert.Zero(t, stored.FeedbackCheckRunID)

	app := &fakeAppClient{createID: 9004}
	proc.App = app
	require.NoError(t, proc.Handle(event))
	require.Len(t, app.creates, 1)
	assert.Equal(t, int64(9004), storedFeedbackID(t, checks, repoID, "check_run", "7654322"))
}

func TestProcessor_CheckSuiteWithoutAppFailsInsteadOfReturningSuccess(t *testing.T) {
	proc, _, _, _ := linkedCheckProcessor(t)
	body := []byte(`{
		"action":"requested",
		"check_suite":{"head_sha":"dddddddddddddddddddddddddddddddddddddddd"},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}},
		"installation":{"id":42}
	}`)

	err := proc.Handle(githubwebhook.EventContext{
		DeliveryID: "missing-app-check-suite",
		Event:      "check_suite",
		Body:       body,
		Log:        discardLogger(),
	})
	require.ErrorContains(t, err, "github app client is not configured")
}

func TestProcessor_OwnFeedbackCheckCompletionIsIgnored(t *testing.T) {
	proc, checks, pool, repoID := linkedCheckProcessor(t)
	app := &fakeAppClient{createID: 9003}
	proc.App = app
	body := []byte(`{
		"action":"completed",
		"check_run":{
			"id":9001,
			"name":"武陵监听 / GitHub Actions / CI",
			"external_id":"wuling-monitor:workflow_run:456789",
			"head_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"status":"completed",
			"conclusion":"failure",
			"completed_at":"2026-08-24T11:00:00Z",
			"app":{"id":3713023,"slug":"wuling-devops","name":"Wuling DevOps"}
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)

	require.NoError(t, proc.Handle(githubwebhook.EventContext{
		DeliveryID: "own-feedback",
		Event:      "check_run",
		Body:       body,
		Log:        discardLogger(),
	}))
	assert.Empty(t, app.creates)
	assert.Empty(t, app.updates)
	stored, err := checks.Get(context.Background(), repoID, "check_run", "9001")
	require.NoError(t, err)
	assert.Nil(t, stored)
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM notification_outbox`).Scan(&count))
	assert.Zero(t, count)
}

func TestCheckStore_OutOfOrderCompletionPreservesLatestState(t *testing.T) {
	_, checks, _, repoID := linkedCheckProcessor(t)
	ctx := context.Background()
	base := githubwebhook.CheckCompletion{
		RepoID:      repoID,
		Source:      "workflow_run",
		ExternalID:  "out-of-order-run",
		Name:        "CI",
		Provider:    "github-actions",
		HeadSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Conclusion:  "failure",
		Color:       githubwebhook.CheckColorRed,
		Attempt:     2,
		DetailsURL:  "https://github.com/acme/app/actions/runs/out-of-order-run",
		CompletedAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC),
	}
	saved, err := checks.Upsert(ctx, base)
	require.NoError(t, err)
	assert.Equal(t, 2, saved.Attempt)

	olderAttempt := base
	olderAttempt.Attempt = 1
	olderAttempt.Name = "stale CI"
	olderAttempt.Provider = "stale-provider"
	olderAttempt.HeadSHA = "dddddddddddddddddddddddddddddddddddddddd"
	olderAttempt.DetailsURL = "https://example.test/stale"
	olderAttempt.Conclusion = "success"
	olderAttempt.Color = githubwebhook.CheckColorGreen
	olderAttempt.CompletedAt = base.CompletedAt.Add(time.Hour)
	saved, err = checks.Upsert(ctx, olderAttempt)
	require.NoError(t, err)
	assert.Equal(t, 2, saved.Attempt)
	assert.Equal(t, base.Name, saved.Name)
	assert.Equal(t, base.Provider, saved.Provider)
	assert.Equal(t, base.HeadSHA, saved.HeadSHA)
	assert.Equal(t, base.DetailsURL, saved.DetailsURL)
	assert.Equal(t, "failure", saved.Conclusion)

	olderCompletion := base
	olderCompletion.Conclusion = "success"
	olderCompletion.Color = githubwebhook.CheckColorGreen
	olderCompletion.CompletedAt = base.CompletedAt.Add(-time.Minute)
	saved, err = checks.Upsert(ctx, olderCompletion)
	require.NoError(t, err)
	assert.True(t, saved.CompletedAt.Equal(base.CompletedAt))
	assert.Equal(t, "failure", saved.Conclusion)

	newerCompletion := base
	newerCompletion.Name = "CI rerun"
	newerCompletion.DetailsURL = "https://example.test/newer"
	newerCompletion.Conclusion = "success"
	newerCompletion.Color = githubwebhook.CheckColorGreen
	newerCompletion.CompletedAt = base.CompletedAt.Add(time.Minute)
	saved, err = checks.Upsert(ctx, newerCompletion)
	require.NoError(t, err)
	assert.True(t, saved.CompletedAt.Equal(newerCompletion.CompletedAt))
	assert.Equal(t, newerCompletion.Name, saved.Name)
	assert.Equal(t, newerCompletion.DetailsURL, saved.DetailsURL)
	assert.Equal(t, "success", saved.Conclusion)
	assert.Equal(t, githubwebhook.CheckColorGreen, saved.Color)
}

func TestProcessor_NotificationFailureRollsBackCheckAndOutbox(t *testing.T) {
	proc, checks, pool, repoID := linkedCheckProcessor(t)
	proc.Notifications = rollbackPublisher{outbox: &notification.Outbox{Pool: pool}}
	body := []byte(`{
		"action":"completed",
		"check_run":{
			"id":222,
			"name":"transactional check",
			"head_sha":"cccccccccccccccccccccccccccccccccccccccc",
			"status":"completed",
			"conclusion":"success",
			"completed_at":"2026-08-24T11:00:00Z",
			"app":{"id":15368,"slug":"github-actions"}
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)
	err := proc.Handle(githubwebhook.EventContext{
		DeliveryID: "check-transaction-rollback",
		Event:      "check_run",
		Body:       body,
		Log:        discardLogger(),
	})
	require.ErrorContains(t, err, "force notification rollback")

	got, err := checks.Get(context.Background(), repoID, "check_run", "222")
	require.NoError(t, err)
	assert.Nil(t, got)
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM notification_outbox`).Scan(&count))
	assert.Zero(t, count)
}

func TestProcessor_IncompleteCheckRunIsIgnored(t *testing.T) {
	proc, checks, pool, repoID := linkedCheckProcessor(t)
	body := []byte(`{
		"action":"created",
		"check_run":{
			"id":111,
			"name":"lint",
			"status":"queued",
			"app":{"id":15368,"slug":"github-actions"}
		},
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}}
	}`)
	require.NoError(t, proc.Handle(githubwebhook.EventContext{
		DeliveryID: "check-not-complete",
		Event:      "check_run",
		Body:       body,
		Log:        discardLogger(),
	}))

	got, err := checks.Get(context.Background(), repoID, "check_run", "111")
	require.NoError(t, err)
	assert.Nil(t, got)
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM notification_outbox`).Scan(&count))
	assert.Zero(t, count)
}

func linkedCheckProcessor(t *testing.T) (*githubwebhook.Processor, *githubwebhook.CheckStore, *db.Pool, uuid.UUID) {
	t.Helper()
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	users := userstore.New(pool)
	ctx := context.Background()
	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "checks-owner", Email: "checks-owner@example.test",
	})
	require.NoError(t, err)
	project, err := users.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID, Slug: "checks", DisplayName: "Checks",
	})
	require.NoError(t, err)
	repo, err := users.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID: project.ID, Slug: "app", DisplayName: "App",
	})
	require.NoError(t, err)
	links := &githubwebhook.LinkStore{Pool: pool}
	_, err = links.Upsert(ctx, githubwebhook.RepoLink{
		InstallationID: 42,
		Owner:          "acme",
		Name:           "app",
		OrgID:          org.ID,
		ProjectID:      project.ID,
		RepoID:         repo.ID,
	})
	require.NoError(t, err)
	checks := &githubwebhook.CheckStore{Pool: pool}
	return &githubwebhook.Processor{
		AppID:         3713023,
		Links:         links,
		Checks:        checks,
		Notifications: &notification.Outbox{Pool: pool},
	}, checks, pool, repo.ID
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func storedFeedbackID(
	t *testing.T,
	checks *githubwebhook.CheckStore,
	repoID uuid.UUID,
	source, externalID string,
) int64 {
	t.Helper()
	stored, err := checks.Get(context.Background(), repoID, source, externalID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	return stored.FeedbackCheckRunID
}

type rollbackPublisher struct {
	outbox *notification.Outbox
}

func (p rollbackPublisher) PublishTx(ctx context.Context, tx pgx.Tx, event notification.Event) error {
	if err := p.outbox.PublishTx(ctx, tx, event); err != nil {
		return err
	}
	return errors.New("force notification rollback")
}

type checkCreateCall struct {
	token string
	owner string
	repo  string
	body  githubapp.CreateCheckRunRequest
}

type checkUpdateCall struct {
	token      string
	owner      string
	repo       string
	checkRunID int64
	body       githubapp.UpdateCheckRunRequest
}

type fakeAppClient struct {
	createID        int64
	installationIDs []int64
	creates         []checkCreateCall
	updates         []checkUpdateCall
}

func (f *fakeAppClient) InstallationToken(installationID int64) (string, error) {
	f.installationIDs = append(f.installationIDs, installationID)
	return "installation-token", nil
}

func (f *fakeAppClient) CreateCheckRun(
	token, owner, repo string,
	body githubapp.CreateCheckRunRequest,
) (int64, error) {
	f.creates = append(f.creates, checkCreateCall{
		token: token,
		owner: owner,
		repo:  repo,
		body:  body,
	})
	return f.createID, nil
}

func (f *fakeAppClient) UpdateCheckRun(
	token, owner, repo string,
	checkRunID int64,
	body githubapp.UpdateCheckRunRequest,
) error {
	f.updates = append(f.updates, checkUpdateCall{
		token:      token,
		owner:      owner,
		repo:       repo,
		checkRunID: checkRunID,
		body:       body,
	})
	return nil
}
