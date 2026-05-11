//go:build cgo

package insightstore_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/insightstore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// TestIndexer_PopulatesActivity walks a fresh bare repo seeded with two
// commits and asserts the per-day activity rollup picks them up plus the
// contributors list aggregates by author email.
func TestIndexer_PopulatesActivityAndContributors(t *testing.T) {
	require.NoError(t, git.Init())

	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)
	insights := insightstore.New(pool, slog.Default())

	ctx := context.Background()

	// Bootstrap user/org/project/repo. Use the same path the layout would
	// produce so the Languages helper can read it back through the wrapper.
	hash, err := auth.HashPassword("dontcare")
	require.NoError(t, err)
	user, org, err := store.CreateUser(ctx, userstore.CreateUserParams{
		Username: "ind", Email: "ind@example.test", PasswordHash: hash,
	})
	require.NoError(t, err)
	proj, err := store.CreateProject(ctx, userstore.CreateProjectParams{OrgID: org.ID, Slug: "p"})
	require.NoError(t, err)
	repo, err := store.CreateRepo(ctx, userstore.CreateRepoParams{ProjectID: proj.ID, Slug: "r"})
	require.NoError(t, err)
	_ = user

	repoPath := filepath.Join(t.TempDir(), "repo.git")
	require.NoError(t, git.InitBare(repoPath, "main"))

	sig1 := git.Author{Name: "Alice", Email: "alice@x", When: time.Now().UTC()}
	sig2 := git.Author{Name: "BOB", Email: "BOB@X", When: time.Now().UTC()}

	_, err = git.CommitFile(repoPath, "refs/heads/main", "README.md", []byte("hello\n"), sig1, "init")
	require.NoError(t, err)
	_, err = git.CommitFile(repoPath, "refs/heads/main", "main.go", []byte("package main\nfunc main() {}\n"), sig2, "add main")
	require.NoError(t, err)
	_, err = git.CommitFile(repoPath, "refs/heads/main", "extra.go", []byte("package main\n"), sig2, "add extra")
	require.NoError(t, err)

	require.NoError(t, insights.IndexNow(ctx, repo.ID, repoPath))

	// Activity rollup: at least 3 commits in the last 7 days.
	days, err := insights.Activity(ctx, proj.ID, 7*24*time.Hour)
	require.NoError(t, err)
	var commits int64
	for _, d := range days {
		commits += d.Commits
	}
	assert.GreaterOrEqual(t, commits, int64(3), "expected >=3 commits in 7d activity")

	// Contributors should de-dup BOB/B  by lowered email.
	contribs, err := insights.Contributors(ctx, repo.ID, 7*24*time.Hour, 10)
	require.NoError(t, err)
	require.Len(t, contribs, 2, "alice + bob (BOB collapsed)")
	// Sorted by commits DESC; BOB has 2 commits, Alice has 1.
	assert.Equal(t, "bob@x", contribs[0].Email)
	assert.Equal(t, int64(2), contribs[0].Commits)
	assert.Equal(t, "alice@x", contribs[1].Email)
	assert.Equal(t, int64(1), contribs[1].Commits)

	// Languages: Markdown + Go bytes are both present.
	langs, err := insights.Languages(repoPath, "", "main")
	require.NoError(t, err)
	assert.Greater(t, langs.Bytes["Go"], int64(0), "expected non-zero Go bytes")
	assert.Greater(t, langs.Bytes["Markdown"], int64(0), "expected non-zero Markdown bytes")
	assert.Equal(t, int64(2), langs.Files["Go"], "two .go files seeded")
}

// TestIndexer_Idempotent verifies the ON CONFLICT DO NOTHING path: re-running
// IndexNow on an already-indexed repo doesn't double-count commits.
func TestIndexer_Idempotent(t *testing.T) {
	require.NoError(t, git.Init())

	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)
	insights := insightstore.New(pool, slog.Default())

	ctx := context.Background()
	hash, _ := auth.HashPassword("x")
	_, org, _ := store.CreateUser(ctx, userstore.CreateUserParams{
		Username: "idem", Email: "idem@x", PasswordHash: hash,
	})
	proj, _ := store.CreateProject(ctx, userstore.CreateProjectParams{OrgID: org.ID, Slug: "p"})
	repo, _ := store.CreateRepo(ctx, userstore.CreateRepoParams{ProjectID: proj.ID, Slug: "r"})

	repoPath := filepath.Join(t.TempDir(), "repo.git")
	require.NoError(t, git.InitBare(repoPath, "main"))
	sig := git.Author{Name: "T", Email: "t@x", When: time.Now().UTC()}
	_, err := git.CommitFile(repoPath, "refs/heads/main", "a.md", []byte("a"), sig, "1")
	require.NoError(t, err)

	require.NoError(t, insights.IndexNow(ctx, repo.ID, repoPath))
	require.NoError(t, insights.IndexNow(ctx, repo.ID, repoPath))

	var n int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM repo_commit_index WHERE repo_id = $1`, repo.ID).Scan(&n))
	assert.Equal(t, int64(1), n, "second IndexNow must not duplicate rows")
}
