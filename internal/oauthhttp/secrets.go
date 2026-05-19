// oauthhttp/secrets.go — small helpers shared across token / app handlers
// for generating client secrets and client_id strings.
package oauthhttp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
)

// randomClientID returns a short, opaque, URL-safe identifier shaped like
// `app_<24 hex chars>`. The `app_` prefix distinguishes user-created apps
// from first-party seeds (whose client_id is human-readable, e.g.
// "wuling-desktop").
func randomClientID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "app_" + hex.EncodeToString(buf), nil
}

// newPrefixedSecret mints a confidential client secret. The HMAC is the
// shape stored in the DB; the raw form is shown to the operator exactly once
// (on create / reset).
func newPrefixedSecret(h *auth.HMACHasher, prefix string, nBytes int) (raw, hash string, err error) {
	if nBytes < 16 {
		return "", "", errors.New("secret entropy too low")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = prefix + hex.EncodeToString(buf)
	hash = h.Hash(raw)
	return raw, hash, nil
}
