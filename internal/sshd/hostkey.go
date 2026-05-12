package sshd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gossh "golang.org/x/crypto/ssh"
)

// LoadOrCreateHostKey loads the ed25519 private key at path, or generates a
// fresh one and persists it (mode 0o600) when the file doesn't exist. The
// parent directory is created with mode 0o700 if needed.
//
// Other key types (RSA, ECDSA) are accepted when loading an existing file —
// we just always generate ed25519 ourselves. Operators that want to mount
// a pre-baked key are free to use whatever OpenSSH supports.
func LoadOrCreateHostKey(path string) (gossh.Signer, error) {
	if path == "" {
		return nil, errors.New("sshd: host key path is empty")
	}
	if signer, err := loadHostKey(path); err == nil {
		return signer, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create host key dir: %w", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519: %w", err)
	}
	// MarshalPrivateKey returns an OpenSSH-format PEM block, which is what
	// every OpenSSH tool emits these days — and what gossh.ParsePrivateKey
	// expects on the load side.
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal ed25519 key: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("write host key: %w", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("derive signer: %w", err)
	}
	return signer, nil
}

func loadHostKey(path string) (gossh.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, perr := gossh.ParsePrivateKey(raw)
	if perr != nil {
		return nil, fmt.Errorf("parse host key %s: %w", path, perr)
	}
	return signer, nil
}
