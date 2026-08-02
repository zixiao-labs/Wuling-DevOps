//go:build cgo

package orgconfig_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

const (
	configProject = "config"
	configRepo    = "config"
)

func setupStore(t *testing.T) (*orgconfig.Store, *userstore.Store, context.Context) {
	t.Helper()
	require.NoError(t, git.Init())

	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	users := userstore.New(pool)
	layout := repostore.New(t.TempDir())
	store := orgconfig.New(users, layout, configProject, configRepo)
	return store, users, t.Context()
}

func TestRead_WhenNothingExists(t *testing.T) {
	store, users, ctx := setupStore(t)

	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "cfg-user", Email: "cfg-user@example.test",
	})
	require.NoError(t, err)

	f, err := store.Read(ctx, org.ID, orgconfig.RunnerConfigPath)
	require.NoError(t, err)
	assert.False(t, f.Exists())
	assert.Empty(t, f.BlobSHA)
}

func TestWrite_AutoCreatesProjectAndRepo(t *testing.T) {
	store, users, ctx := setupStore(t)

	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "cfg-create", Email: "cfg-create@example.test",
	})
	require.NoError(t, err)

	content := []byte("version: 1\npools: []\n")
	empty := ""
	res, err := store.Write(ctx, orgconfig.WriteParams{
		OrgID: org.ID, Name: orgconfig.RunnerConfigPath, Content: content,
		Message: "init", Author: git.Author{Name: "Tester", Email: "t@example.test", When: time.Now().UTC()},
		BaseBlobSHA: &empty, EnsureRepo: true,
	})
	require.NoError(t, err)
	assert.True(t, res.CreatedProject)
	assert.True(t, res.CreatedRepo)
	require.True(t, res.File.Exists())
	assert.Equal(t, content, res.File.Content)

	project, err := users.GetProjectBySlug(ctx, org.ID, configProject)
	require.NoError(t, err)
	repo, err := users.GetRepoBySlug(ctx, project.ID, configRepo)
	require.NoError(t, err)
	assert.False(t, repo.IsEmpty)
}

func TestWrite_StaleBaseBlobSHA_Conflict(t *testing.T) {
	store, users, ctx := setupStore(t)

	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "cfg-stale", Email: "cfg-stale@example.test",
	})
	require.NoError(t, err)

	content := []byte("version: 1\npools: []\n")
	empty := ""
	_, err = store.Write(ctx, orgconfig.WriteParams{
		OrgID: org.ID, Name: orgconfig.RunnerConfigPath, Content: content,
		Message: "init", Author: git.Author{Name: "Tester", Email: "t@example.test", When: time.Now().UTC()},
		BaseBlobSHA: &empty, EnsureRepo: true,
	})
	require.NoError(t, err)

	stale := "0000000000000000000000000000000000000000"
	_, err = store.Write(ctx, orgconfig.WriteParams{
		OrgID: org.ID, Name: orgconfig.RunnerConfigPath,
		Content: []byte("version: 1\npools: []\n# edited\n"),
		Message: "edit", Author: git.Author{Name: "Tester", Email: "t@example.test", When: time.Now().UTC()},
		BaseBlobSHA: &stale, EnsureRepo: true,
	})
	require.Error(t, err)
	ae := apperr.As(err)
	require.NotNil(t, ae)
	assert.Equal(t, apperr.CodeConflict, ae.Code)
}

func TestWrite_BaseBlobSHA_EmptyAgainstExisting_Conflict(t *testing.T) {
	store, users, ctx := setupStore(t)

	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "cfg-empty-base", Email: "cfg-empty-base@example.test",
	})
	require.NoError(t, err)

	content := []byte("version: 1\npools: []\n")
	empty := ""
	_, err = store.Write(ctx, orgconfig.WriteParams{
		OrgID: org.ID, Name: orgconfig.RunnerConfigPath, Content: content,
		Message: "init", Author: git.Author{Name: "Tester", Email: "t@example.test", When: time.Now().UTC()},
		BaseBlobSHA: &empty, EnsureRepo: true,
	})
	require.NoError(t, err)

	_, err = store.Write(ctx, orgconfig.WriteParams{
		OrgID: org.ID, Name: orgconfig.RunnerConfigPath,
		Content: []byte("version: 1\npools: []\n# edited\n"),
		Message: "edit", Author: git.Author{Name: "Tester", Email: "t@example.test", When: time.Now().UTC()},
		BaseBlobSHA: &empty, EnsureRepo: true,
	})
	require.Error(t, err)
	ae := apperr.As(err)
	require.NotNil(t, ae)
	assert.Equal(t, apperr.CodeConflict, ae.Code)
}

func TestWrite_Unchanged_ShortCircuits(t *testing.T) {
	store, users, ctx := setupStore(t)

	_, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "cfg-unchanged", Email: "cfg-unchanged@example.test",
	})
	require.NoError(t, err)

	content := []byte("version: 1\npools: []\n")
	empty := ""
	res1, err := store.Write(ctx, orgconfig.WriteParams{
		OrgID: org.ID, Name: orgconfig.RunnerConfigPath, Content: content,
		Message: "init", Author: git.Author{Name: "Tester", Email: "t@example.test", When: time.Now().UTC()},
		BaseBlobSHA: &empty, EnsureRepo: true,
	})
	require.NoError(t, err)
	require.True(t, res1.File.Exists())

	base := res1.File.BlobSHA
	res2, err := store.Write(ctx, orgconfig.WriteParams{
		OrgID: org.ID, Name: orgconfig.RunnerConfigPath, Content: content,
		Message: "noop", Author: git.Author{Name: "Tester", Email: "t@example.test", When: time.Now().UTC()},
		BaseBlobSHA: &base, EnsureRepo: true,
	})
	require.NoError(t, err)
	assert.True(t, res2.Unchanged)
	assert.Equal(t, res1.File.BlobSHA, res2.File.BlobSHA)
}
