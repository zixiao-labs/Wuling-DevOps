package sshd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

func TestLoadOrCreateHostKey_Generates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "host_ed25519")

	signer, err := LoadOrCreateHostKey(path)
	require.NoError(t, err)
	require.NotNil(t, signer)
	assert.Equal(t, "ssh-ed25519", signer.PublicKey().Type())

	st, err := os.Stat(path)
	require.NoError(t, err, "host key file should be persisted")
	// 0o600 means owner-read+write only. Mask off file-type bits.
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestLoadOrCreateHostKey_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host_ed25519")

	first, err := LoadOrCreateHostKey(path)
	require.NoError(t, err)
	firstFP := gossh.FingerprintSHA256(first.PublicKey())

	// Second call must load the existing key, not generate a new one.
	second, err := LoadOrCreateHostKey(path)
	require.NoError(t, err)
	secondFP := gossh.FingerprintSHA256(second.PublicKey())

	assert.Equal(t, firstFP, secondFP, "second load must return the same key")
}

func TestLoadOrCreateHostKey_EmptyPath(t *testing.T) {
	_, err := LoadOrCreateHostKey("")
	require.Error(t, err)
}

func TestLoadOrCreateHostKey_RejectsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host_ed25519")
	require.NoError(t, os.WriteFile(path, []byte("not a pem block"), 0o600))

	_, err := LoadOrCreateHostKey(path)
	require.Error(t, err, "corrupt key file should not silently regenerate")
}
