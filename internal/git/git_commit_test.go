//go:build cgo

package git

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommitFile_RoundTrip exercises the new wg_repo_commit_file +
// wg_repo_delete_file paths end to end through a temp-dir bare repo.
//
// Locked to the cgo build because the stub returns ErrCGOUnsupported and the
// assertions would all immediately fail. We don't gate on -short; libgit2 is
// in-process and the test runs in a few ms.
func TestCommitFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, InitBare(dir, "main"))

	sig := Author{Name: "Tester", Email: "tester@example.test", When: time.Now().UTC()}

	// First commit creates the ref.
	oid1, err := CommitFile(dir, "refs/heads/main", "Home.md", []byte("# Hi\n"), sig, "init Home")
	require.NoError(t, err)
	require.Len(t, oid1, 40)

	// Nested path on a follow-up commit verifies that the index seeded from
	// the parent tree picks up subdirectory writes correctly.
	oid2, err := CommitFile(dir, "refs/heads/main", "docs/usage.md", []byte("# Usage\n"), sig, "docs")
	require.NoError(t, err)
	require.NotEqual(t, oid1, oid2)

	// Both blobs should be present in the tree at HEAD.
	headOID, err := Resolve(dir, "refs/heads/main")
	require.NoError(t, err)
	assert.Equal(t, oid2, headOID)

	root, err := ReadTree(dir, headOID)
	require.NoError(t, err)
	rootNames := entryNames(root)
	assert.Contains(t, rootNames, "Home.md")
	assert.Contains(t, rootNames, "docs")

	// Updating an existing path: same path, different content. Ensures the
	// blob OID swaps in via git_index_add rather than producing a duplicate.
	oid3, err := CommitFile(dir, "refs/heads/main", "Home.md", []byte("# Hi (v2)\n"), sig, "edit Home")
	require.NoError(t, err)
	root3, err := ReadTree(dir, oid3)
	require.NoError(t, err)
	var homeOID string
	for _, e := range root3 {
		if e.Name == "Home.md" {
			homeOID = e.OID
		}
	}
	require.NotEmpty(t, homeOID)
	blob, err := ReadBlob(dir, homeOID)
	require.NoError(t, err)
	assert.Equal(t, "# Hi (v2)\n", string(blob.Data))

	// Deletion removes the blob and creates a real commit.
	oid4, err := DeleteFile(dir, "refs/heads/main", "Home.md", sig, "rm Home")
	require.NoError(t, err)
	root4, err := ReadTree(dir, oid4)
	require.NoError(t, err)
	assert.NotContains(t, entryNames(root4), "Home.md", "Home.md should be gone after delete")

	// Deleting a missing path surfaces a "not found:" error so the Go layer
	// can map to apperr.NotFound.
	_, err = DeleteFile(dir, "refs/heads/main", "ghosts/never-existed.md", sig, "rm")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not found"),
		"expected 'not found' marker in error: %v", err)

	// Walk the commit history. Should be init -> docs -> edit -> rm (4 entries).
	commits, err := Log(dir, "", 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(commits), 4, "expected at least 4 commits in log")
	assert.Equal(t, "rm Home", strings.TrimSpace(commits[0].Message))
}

func entryNames(entries []TreeEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}
