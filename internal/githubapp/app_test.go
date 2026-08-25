package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"strings"
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

func TestRepositoryInstallation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	client := New(3713023, key, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, req.Method)
		assert.Equal(t, "/repos/acme/app/installation", req.URL.Path)
		assert.True(t, strings.HasPrefix(req.Header.Get("Authorization"), "Bearer "))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"id":4242}`)),
		}, nil
	})})

	id, err := client.RepositoryInstallation("acme", "app")
	require.NoError(t, err)
	assert.Equal(t, int64(4242), id)
}

func TestRepositoryInstallation_NotInstalled(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	client := New(3713023, key, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"message":"Not Found"}`)),
		}, nil
	})})

	_, err = client.RepositoryInstallation("acme", "app")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRepositoryInstallationNotFound))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
