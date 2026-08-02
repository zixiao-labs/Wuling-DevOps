package githubwebhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVerifySignature(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"zen":"Keep it logically awesome."}`)
	sig := SignBody(secret, body)

	assert.True(t, VerifySignature(secret, body, sig))
	assert.False(t, VerifySignature(secret, body, "sha256=deadbeef"))
	assert.False(t, VerifySignature(secret, body, "sha1=abc"))
	assert.False(t, VerifySignature(secret, append(body, '!'), sig))
	assert.False(t, VerifySignature("", body, sig))
	assert.False(t, VerifySignature(secret, body, ""))
}
