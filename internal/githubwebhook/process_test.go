package githubwebhook_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/githubwebhook"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

func TestProcessor_PushUnlinked_NoError(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)

	proc := &githubwebhook.Processor{
		AppID:  3713023,
		Links:  &githubwebhook.LinkStore{Pool: pool},
		Layout: repostore.New(t.TempDir()),
	}
	body := []byte(`{
		"ref":"refs/heads/main",
		"before":"0000000000000000000000000000000000000000",
		"after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"deleted":false,
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}},
		"installation":{"id":99}
	}`)
	err := proc.Handle(githubwebhook.EventContext{
		DeliveryID: "d1",
		Event:      "push",
		Body:       body,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
}

func TestProcessor_PushLinkedWithoutApp_NoError(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	users := userstore.New(pool)
	ctx := context.Background()

	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "push-no-app", Email: "push-no-app@example.test",
	})
	require.NoError(t, err)
	project, err := users.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID, Slug: "p", DisplayName: "P",
	})
	require.NoError(t, err)
	repo, err := users.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID: project.ID, Slug: "r", DisplayName: "R",
	})
	require.NoError(t, err)

	store := &githubwebhook.LinkStore{Pool: pool}
	_, err = store.Upsert(ctx, githubwebhook.RepoLink{
		InstallationID: 99,
		Owner:          "acme",
		Name:           "app",
		OrgID:          org.ID,
		ProjectID:      project.ID,
		RepoID:         repo.ID,
	})
	require.NoError(t, err)

	proc := &githubwebhook.Processor{
		AppID:  3713023,
		App:    nil,
		Links:  store,
		Layout: repostore.New(t.TempDir()),
	}
	body := []byte(`{
		"ref":"refs/heads/main",
		"before":"0000000000000000000000000000000000000000",
		"after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"deleted":false,
		"repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"}},
		"installation":{"id":99}
	}`)
	err = proc.Handle(githubwebhook.EventContext{
		DeliveryID: "d-push-no-app",
		Event:      "push",
		Body:       body,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
}

func TestLinkStore_UpsertAndLookup(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	users := userstore.New(pool)
	ctx := context.Background()

	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "link-owner", Email: "link-owner@example.test",
	})
	require.NoError(t, err)
	project, err := users.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID, Slug: "p", DisplayName: "P",
	})
	require.NoError(t, err)
	repo, err := users.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID: project.ID, Slug: "r", DisplayName: "R",
	})
	require.NoError(t, err)

	store := &githubwebhook.LinkStore{Pool: pool}
	link, err := store.Upsert(ctx, githubwebhook.RepoLink{
		InstallationID: 42,
		Owner:          "Acme",
		Name:           "App",
		OrgID:          org.ID,
		ProjectID:      project.ID,
		RepoID:         repo.ID,
	})
	require.NoError(t, err)
	assert.True(t, link.Active)

	got, err := store.GetByFullName(ctx, "acme", "app")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, repo.ID, got.RepoID)
	assert.Equal(t, int64(42), got.InstallationID)
}
