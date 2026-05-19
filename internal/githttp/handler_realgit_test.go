package githttp

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// realGitRequired skips when -short or when git isn't on PATH.
func realGitRequired(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real-git integration test in -short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// initBareRepoOnDisk creates an actual bare git repository at layoutPath so
// `git-upload-pack`/`git-receive-pack` have something to talk to. Pin the
// initial branch to "main" — older Ubuntu runners default to "master", which
// would leave HEAD pointing at a ref that the test source repo never pushes,
// and `git clone` would silently produce an empty worktree.
func initBareRepoOnDisk(t *testing.T, layoutPath string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(layoutPath), 0o755))
	cmd := exec.Command("git", "init", "--bare", "--initial-branch=main", layoutPath)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "git init --bare failed")
}

// runGit runs `git ...` in dir with the given env additions and surfaces
// stderr on failure.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// authedRepoURL injects user:pat@ into a server URL.
func authedRepoURL(t *testing.T, server, username, pat, orgSlug, projSlug, repoSlug string) string {
	t.Helper()
	u, err := url.Parse(server)
	require.NoError(t, err)
	u.User = url.UserPassword(username, pat)
	u.Path = fmt.Sprintf("/%s/%s/%s.git", orgSlug, projSlug, repoSlug)
	return u.String()
}

func TestRealGit_CloneEmpty(t *testing.T) {
	realGitRequired(t)
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)

	root := t.TempDir()
	layout := repostore.New(root)
	seed := seedFixture(t, store, "private", []string{"repo:read", "repo:write"})

	repo, projID, orgID, err := store.ResolveRepoPath(context.Background(), seed.orgSlug, seed.projSlug, seed.repoSlug)
	require.NoError(t, err)
	initBareRepoOnDisk(t, layout.Path(orgID, projID, repo.ID))

	h := &Handler{
		Store:    store,
		Layout:   layout,
		PATReslv: &realPATResolver{Store: store},
		OATReslv: denyOATResolver{},
	}
	r := chi.NewRouter()
	h.Mount(r)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)

	cloneDir := filepath.Join(t.TempDir(), "clone")
	cloneURL := authedRepoURL(t, server.URL, seed.username, seed.patRaw, seed.orgSlug, seed.projSlug, seed.repoSlug)
	runGit(t, "", "clone", cloneURL, cloneDir)

	// Empty clone: HEAD should resolve to nothing committable, but the .git
	// directory exists.
	st, err := os.Stat(filepath.Join(cloneDir, ".git"))
	require.NoError(t, err)
	assert.True(t, st.IsDir())
}

func TestRealGit_PushAndClone(t *testing.T) {
	realGitRequired(t)
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)

	root := t.TempDir()
	layout := repostore.New(root)
	seed := seedFixture(t, store, "private", []string{"repo:read", "repo:write"})

	repo, projID, orgID, err := store.ResolveRepoPath(context.Background(), seed.orgSlug, seed.projSlug, seed.repoSlug)
	require.NoError(t, err)
	initBareRepoOnDisk(t, layout.Path(orgID, projID, repo.ID))

	h := &Handler{
		Store:    store,
		Layout:   layout,
		PATReslv: &realPATResolver{Store: store},
		OATReslv: denyOATResolver{},
	}
	r := chi.NewRouter()
	h.Mount(r)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)

	pushDir := filepath.Join(t.TempDir(), "src")
	require.NoError(t, os.MkdirAll(pushDir, 0o755))
	runGit(t, pushDir, "init", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(pushDir, "README.md"), []byte("# hi\n"), 0o644))
	runGit(t, pushDir, "add", "README.md")
	runGit(t, pushDir, "commit", "-m", "first")
	pushURL := authedRepoURL(t, server.URL, seed.username, seed.patRaw, seed.orgSlug, seed.projSlug, seed.repoSlug)
	runGit(t, pushDir, "remote", "add", "origin", pushURL)
	runGit(t, pushDir, "push", "-u", "origin", "main")

	// Reclone in a separate dir; it should contain the pushed commit.
	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", pushURL, cloneDir)

	gotREADME, err := os.ReadFile(filepath.Join(cloneDir, "README.md"))
	require.NoError(t, err, "cloned worktree must contain the pushed file")
	assert.Equal(t, "# hi\n", string(gotREADME))

	// Repo should now be marked non-empty.
	repo2, _, _, err := store.ResolveRepoPath(context.Background(), seed.orgSlug, seed.projSlug, seed.repoSlug)
	require.NoError(t, err)
	assert.False(t, repo2.IsEmpty, "MarkRepoNotEmpty should have flipped is_empty")
}
