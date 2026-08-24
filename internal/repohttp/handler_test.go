package repohttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

type deleteFixture struct {
	router   http.Handler
	store    *userstore.Store
	repo     *model.Repo
	repoPath string
	apiPath  string
	token    string
}

func newDeleteFixture(t *testing.T, role string) deleteFixture {
	t.Helper()
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ctx := t.Context()
	store := userstore.New(pool)

	owner, org, err := store.CreateUser(ctx, userstore.CreateUserParams{
		Username: "repo-delete-owner", Email: "repo-delete-owner@example.com",
	})
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID, Slug: "delivery",
	})
	require.NoError(t, err)
	repo, err := store.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID: project.ID, Slug: "api",
	})
	require.NoError(t, err)

	actor := owner
	if role != auth.RoleOwner {
		actor, _, err = store.CreateUser(ctx, userstore.CreateUserParams{
			Username: "repo-delete-actor", Email: "repo-delete-actor@example.com",
		})
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)
		`, org.ID, actor.ID, role)
		require.NoError(t, err)
	}

	layout := repostore.New(t.TempDir())
	repoPath := layout.Path(org.ID, project.ID, repo.ID)
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, os.WriteFile(repoPath+"/HEAD", []byte("ref: refs/heads/main\n"), 0o644))

	jwtConfig := config.JWTConfig{
		Secret: "repo-delete-handler-test-secret", Issuer: "repo-delete-handler-test",
		Audience: "repo-delete-handler-test", TTL: time.Hour,
	}
	issuer := auth.NewIssuer(jwtConfig)
	token, _, err := issuer.Issue(actor.ID, actor.Username)
	require.NoError(t, err)

	handler := &Handler{Store: store, Layout: layout, Verifier: auth.NewVerifier(jwtConfig)}
	router := chi.NewRouter()
	router.Route("/api/v1", func(api chi.Router) { handler.Mount(api) })

	return deleteFixture{
		router:   router,
		store:    store,
		repo:     repo,
		repoPath: repoPath,
		apiPath:  "/api/v1/orgs/" + org.Slug + "/projects/" + project.Slug + "/repos/" + repo.Slug,
		token:    token,
	}
}

func (f deleteFixture) delete(t *testing.T) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, f.apiPath, nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, req)
	return response.Code
}

func TestDeleteRepoRemovesRecordAndBareRepository(t *testing.T) {
	fixture := newDeleteFixture(t, auth.RoleOwner)

	require.Equal(t, http.StatusNoContent, fixture.delete(t))
	_, err := fixture.store.GetRepoByID(t.Context(), fixture.repo.ID)
	require.Error(t, err)
	require.Equal(t, apperr.CodeNotFound, apperr.As(err).Code)
	_, err = os.Stat(fixture.repoPath)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestDeleteRepoRequiresMaintainer(t *testing.T) {
	fixture := newDeleteFixture(t, auth.RoleDeveloper)

	require.Equal(t, http.StatusForbidden, fixture.delete(t))
	_, err := fixture.store.GetRepoByID(t.Context(), fixture.repo.ID)
	require.NoError(t, err)
	_, err = os.Stat(fixture.repoPath)
	require.NoError(t, err)
}

func TestDeleteRepoAllowsMaintainer(t *testing.T) {
	fixture := newDeleteFixture(t, auth.RoleMaintainer)

	require.Equal(t, http.StatusNoContent, fixture.delete(t))
	_, err := fixture.store.GetRepoByID(t.Context(), fixture.repo.ID)
	require.Error(t, err)
	require.Equal(t, apperr.CodeNotFound, apperr.As(err).Code)
}
