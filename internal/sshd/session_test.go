package sshd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

func TestParseGitCommand(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		wantSvc  gitService
		wantPath string
		wantErr  string
	}{
		{"upload quoted", "git-upload-pack '/org/proj/repo.git'", uploadPack, "org/proj/repo", ""},
		{"receive quoted", "git-receive-pack '/org/proj/repo.git'", receivePack, "org/proj/repo", ""},
		{"upload bare path", "git-upload-pack org/proj/repo.git", uploadPack, "org/proj/repo", ""},
		{"upload no .git", "git-upload-pack 'org/proj/repo'", uploadPack, "org/proj/repo", ""},
		{"empty", "", "", "", "missing git command"},
		{"bad command", "ls /", "", "", "command not allowed"},
		{"shell metachar", "git-upload-pack '/x; rm -rf /'", "", "", "invalid characters"},
		{"newline in path", "git-upload-pack 'a/b/c\n.git'", "", "", "invalid characters"},
		{"empty path", "git-upload-pack ''", "", "", "empty repository path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, p, err := parseGitCommand(tc.cmd)
			if tc.wantErr != "" {
				require.Error(t, err)
				ae := apperr.As(err)
				require.NotNil(t, ae)
				assert.Contains(t, ae.Message, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSvc, svc)
			assert.Equal(t, tc.wantPath, p)
		})
	}
}

func TestSplitRepoPath(t *testing.T) {
	cases := []struct {
		in              string
		org, proj, repo string
		wantErr         bool
	}{
		{"a/b/c", "a", "b", "c", false},
		{"a/b", "", "", "", true},
		{"a/b/c/d", "", "", "", true},
		{"/a/b/c", "", "", "", true}, // leading slash already stripped upstream; defensive
		{"", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			org, proj, repo, err := splitRepoPath(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.org, org)
			assert.Equal(t, tc.proj, proj)
			assert.Equal(t, tc.repo, repo)
		})
	}
}

func TestGitServiceMethods(t *testing.T) {
	assert.Equal(t, "upload-pack", uploadPack.sub())
	assert.Equal(t, "receive-pack", receivePack.sub())
	assert.False(t, uploadPack.needsWrite())
	assert.True(t, receivePack.needsWrite())
}
