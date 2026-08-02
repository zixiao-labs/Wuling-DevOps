package githubwebhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const signatureHeader = "X-Hub-Signature-256"

// VerifySignature checks GitHub's X-Hub-Signature-256 header (HMAC-SHA256 of
// the raw body). Only SHA-256 is accepted — the legacy X-Hub-Signature
// (SHA-1) is ignored. Comparison is constant-time.
func VerifySignature(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	wantHex := strings.TrimPrefix(header, prefix)
	want, err := hex.DecodeString(wantHex)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	sum := hmac.New(sha256.New, []byte(secret))
	_, _ = sum.Write(body)
	got := sum.Sum(nil)
	return hmac.Equal(got, want)
}

// SignBody produces the X-Hub-Signature-256 value for tests.
func SignBody(secret string, body []byte) string {
	sum := hmac.New(sha256.New, []byte(secret))
	_, _ = sum.Write(body)
	return "sha256=" + hex.EncodeToString(sum.Sum(nil))
}
