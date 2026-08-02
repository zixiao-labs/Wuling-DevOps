package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	c := New(3713023, key, nil)
	tok, err := c.AppJWT()
	require.NoError(t, err)

	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	})
	require.NoError(t, err)
	claims := parsed.Claims.(jwt.MapClaims)
	assert.Equal(t, float64(3713023), claims["iss"])
	exp := int64(claims["exp"].(float64))
	assert.Less(t, time.Now().Unix(), exp)
	assert.LessOrEqual(t, exp-time.Now().Unix(), int64(10*60))
}

func TestMapConclusion(t *testing.T) {
	assert.Equal(t, "failure", MapConclusion("failed"))
	assert.Equal(t, "cancelled", MapConclusion("canceled"))
	assert.Equal(t, "success", MapConclusion("success"))
}

func TestCloneURL(t *testing.T) {
	u := CloneURL("tok", "acme", "app")
	assert.Equal(t, "https://x-access-token:tok@github.com/acme/app.git", u)
}
