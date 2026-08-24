package githubwebhook_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/db"
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
