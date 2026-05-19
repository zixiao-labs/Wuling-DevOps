// Package auth — OAuth provider-side access tokens (OATs).
//
// Where Personal Access Tokens (`wlpat_…`, see auth.go) are long-lived bearers
// hashed with argon2id, OATs (`wloat_…`) are issued by this server in its
// OAuth Authorization Server role and have a fundamentally different security
// model:
//
//   - bound to a specific (user, client, scope set) triple
//   - short-lived (~hours), refreshed by paired refresh tokens
//   - rehashed on *every* request to look up the access_tokens row, so the KDF
//     must be fast — HMAC-SHA256 with a server-held secret, not argon2id.
//
// The server secret never leaves the host, so an attacker who pops the DB
// still can't forge tokens. Leak detection still benefits from the `wloat_`
// prefix appearing in secret-scanning rulesets.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// Token shape constants. The raw token wire format is `prefix + hex(secret)`
// so the leak-scan regex stays trivial and there's no ambiguity when a Git
// CLI puts the whole thing in the HTTP Basic password field.
const (
	OATPrefix     = "wloat_"
	RefreshPrefix = "wlref_"
)

// HMACHasher computes a stable, fast hash for token row-lookup. The secret is
// supplied via WULING_OAUTH_HMAC_SECRET; if empty (dev default), the hash is
// still deterministic but trivially forgeable by an attacker who reads the
// DB. Production config validation rejects an empty secret.
type HMACHasher struct {
	secret []byte
}

// NewHMACHasher returns an HMACHasher with the given secret. The secret may be
// empty during tests.
func NewHMACHasher(secret string) *HMACHasher {
	return &HMACHasher{secret: []byte(secret)}
}

// Hash returns the lowercase hex HMAC-SHA256 of raw under the configured
// secret. Identical inputs always produce identical outputs, so this is safe
// to use as a unique-key in the DB.
func (h *HMACHasher) Hash(raw string) string {
	m := hmac.New(sha256.New, h.secret)
	m.Write([]byte(raw))
	return hex.EncodeToString(m.Sum(nil))
}

// Equal compares two HMAC hex strings in constant time. Use this when looking
// up by hash to defeat timing oracles on user-supplied input.
func (h *HMACHasher) Equal(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// NewOAT mints a fresh OAuth access token. Returns (raw, hash).
func NewOAT(h *HMACHasher) (raw, hash string, err error) {
	return newPrefixedToken(h, OATPrefix, 32)
}

// NewRefreshToken mints a refresh token. Returns (raw, hash).
func NewRefreshToken(h *HMACHasher) (raw, hash string, err error) {
	return newPrefixedToken(h, RefreshPrefix, 32)
}

// NewAuthCode mints an authorization code (raw + hash). The raw form goes
// into the redirect URL; the hash is what the DB row keys on.
func NewAuthCode(h *HMACHasher) (raw, hash string, err error) {
	return newPrefixedToken(h, "", 32)
}

// NewDeviceCode mints a device_code (long random secret). Returns (raw, hash).
// Device codes are not user-facing so we don't bother with a prefix.
func NewDeviceCode(h *HMACHasher) (raw, hash string, err error) {
	return newPrefixedToken(h, "", 32)
}

// userCodeAlphabet is Crockford base32 minus characters that look ambiguous
// (I, L, O, U). Eight characters at 28 symbols ≈ 38 bits of entropy — plenty
// for a 15-minute device-flow window that is rate-limited by `interval`.
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

// NewUserCode returns a human-typable code shaped like `ABCD-1234`, plus the
// raw form (`ABCD1234`) for constant-time comparison.
func NewUserCode() (display, raw string, err error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	var b strings.Builder
	for i, x := range buf {
		b.WriteByte(userCodeAlphabet[int(x)%len(userCodeAlphabet)])
		if i == 3 {
			b.WriteByte('-')
		}
	}
	display = b.String()
	raw = strings.ReplaceAll(display, "-", "")
	return display, raw, nil
}

// NormalizeUserCode upper-cases and strips any dashes from a user-entered
// device code so the lookup is forgiving but the stored form is canonical.
func NormalizeUserCode(in string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(in), "-", ""))
}

// newPrefixedToken generates `prefix + hex(nBytes random)` and returns it
// alongside its HMAC hash. `nBytes >= 16` is required.
func newPrefixedToken(h *HMACHasher, prefix string, nBytes int) (raw, hash string, err error) {
	if nBytes < 16 {
		return "", "", errors.New("token entropy too low")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = prefix + hex.EncodeToString(buf)
	hash = h.Hash(raw)
	return raw, hash, nil
}

// IsOATShaped reports whether s carries the wloat_ prefix. Used by the git
// smart-HTTP handler to dispatch an HTTP Basic password to the OAT resolver.
func IsOATShaped(s string) bool { return strings.HasPrefix(s, OATPrefix) }

// IsPATShaped reports whether s carries the wlpat_ prefix.
func IsPATShaped(s string) bool { return strings.HasPrefix(s, AccessTokenPrefix) }

// MaskUserCode gives an audit-safe representation of a user code: the first
// two characters, then the rest masked. Avoids logging it in the clear while
// still being grep-able when an operator and a user compare notes.
func MaskUserCode(code string) string {
	code = NormalizeUserCode(code)
	if len(code) <= 2 {
		return strings.Repeat("*", len(code))
	}
	return code[:2] + strings.Repeat("*", len(code)-2)
}

// PKCEVerify checks that codeVerifier, when SHA-256'd and base64url-no-pad
// encoded, equals codeChallenge (RFC 7636 §4.6). Returns nil on success.
func PKCEVerify(codeVerifier, codeChallenge string) error {
	if codeVerifier == "" || codeChallenge == "" {
		return errors.New("PKCE: empty verifier or challenge")
	}
	if len(codeVerifier) < 43 || len(codeVerifier) > 128 {
		return errors.New("PKCE: verifier length must be 43..128")
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	enc := base64.RawURLEncoding.EncodeToString(sum[:])
	if !hmac.Equal([]byte(enc), []byte(codeChallenge)) {
		return errors.New("PKCE: challenge mismatch")
	}
	return nil
}
