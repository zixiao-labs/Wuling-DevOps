package authhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/config"
)

func testHandler() *OAuthHandler {
	return &OAuthHandler{
		JWT: config.JWTConfig{Secret: "test-secret-do-not-use-in-prod"},
	}
}

func TestCookieSignVerify_RoundTrip(t *testing.T) {
	h := testHandler()
	in := stateCookie{Nonce: "abc", Verifier: "xyz", Exp: time.Now().Add(time.Minute).Unix()}
	v, err := h.signCookie(in)
	require.NoError(t, err)

	var out stateCookie
	require.NoError(t, h.verifyCookie(v, &out))
	assert.Equal(t, in.Nonce, out.Nonce)
	assert.Equal(t, in.Verifier, out.Verifier)
}

func TestCookieVerify_RejectsTampered(t *testing.T) {
	h := testHandler()
	v, err := h.signCookie(stateCookie{Nonce: "abc", Verifier: "xyz", Exp: 1})
	require.NoError(t, err)

	// Flip a character in the payload portion so HMAC fails.
	tampered := v[:5] + "X" + v[6:]
	var out stateCookie
	err = h.verifyCookie(tampered, &out)
	require.Error(t, err, "tampered payload must fail HMAC check")
}

func TestCookieVerify_RejectsWrongSecret(t *testing.T) {
	signer := &OAuthHandler{JWT: config.JWTConfig{Secret: "secret-a"}}
	verifier := &OAuthHandler{JWT: config.JWTConfig{Secret: "secret-b"}}
	v, err := signer.signCookie(stateCookie{Nonce: "abc"})
	require.NoError(t, err)
	var out stateCookie
	require.Error(t, verifier.verifyCookie(v, &out), "different secret must reject")
}

func TestSanitizeUsername(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"alice", "alice"},
		{"Alice-1", "Alice-1"},
		{"1leadingdigit", "leadingdigit"}, // leading non-letter stripped
		{"_underscore", "underscore"},
		{"foo.bar", "foobar"},                // dot dropped
		{"中文user", "user"},                   // non-ASCII letters dropped
		{"with space", "withspace"},          // space dropped
		{"", ""},
		{"thisusernameisrather-very-extremely-longexceedingthemaxxxxxxxxxxxxxxxxxxxxxxxx",
			"thisusernameisrather-very-extremely-longexceedingthemaxxxxxxxxxx"}, // truncated at 64
	}
	for _, c := range cases {
		got := sanitizeUsername(c.in)
		if c.want == "" {
			assert.Empty(t, got, "input %q", c.in)
			continue
		}
		assert.Equal(t, c.want, got, "input %q", c.in)
		assert.LessOrEqual(t, len(got), 64, "input %q produced %q over 64 chars", c.in, got)
	}
}

func TestFrontendURL(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"", "/oauth/callback", "/oauth/callback"},
		{"/", "/oauth/callback", "/oauth/callback"},
		{"https://devops.example.com", "/oauth/callback", "https://devops.example.com/oauth/callback"},
		{"https://devops.example.com/", "/oauth/callback", "https://devops.example.com/oauth/callback"},
		{"https://devops.example.com/wuling", "oauth/callback", "https://devops.example.com/wuling/oauth/callback"},
	}
	for _, c := range cases {
		h := &OAuthHandler{Cfg: config.OAuthConfig{FrontendBaseURL: c.base}}
		assert.Equal(t, c.want, h.frontendURL(c.path))
	}
}

func TestIsHTTPS(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	assert.False(t, isHTTPS(r))
	r.Header.Set("X-Forwarded-Proto", "https")
	assert.True(t, isHTTPS(r))
}

func TestStart_NotConfiguredReturns503(t *testing.T) {
	h := &OAuthHandler{JWT: config.JWTConfig{Secret: "x"}} // no client_id
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start", nil)
	h.start(rr, r)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestStart_HappyPath_SetsCookieAndRedirects(t *testing.T) {
	h := &OAuthHandler{
		JWT: config.JWTConfig{Secret: "test-secret"},
		Cfg: config.OAuthConfig{
			GithubClientID:     "cid",
			GithubClientSecret: "csecret",
			GithubRedirectURL:  "https://api.example.com/api/v1/auth/oauth/github/callback",
			GithubScopes:       "read:user,user:email",
		},
	}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start?return_to=/dashboard", nil)
	h.start(rr, r)

	require.Equal(t, http.StatusFound, rr.Code, "expected 302 redirect")
	loc := rr.Header().Get("Location")
	require.NotEmpty(t, loc)
	assert.Contains(t, loc, GithubAuthorizeURL)
	assert.Contains(t, loc, "client_id=cid")
	assert.Contains(t, loc, "code_challenge_method=S256")
	assert.Contains(t, loc, "scope=read%3Auser+user%3Aemail", "scopes must be space-separated when sent to GitHub")

	// state cookie present and verifiable.
	var found bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == oauthStateCookie {
			found = true
			var sc stateCookie
			require.NoError(t, h.verifyCookie(c.Value, &sc))
			assert.NotEmpty(t, sc.Nonce)
			assert.NotEmpty(t, sc.Verifier)
			assert.Equal(t, "/dashboard", sc.ReturnTo)
			assert.True(t, c.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
		}
	}
	assert.True(t, found, "start handler must set the state cookie")
}
