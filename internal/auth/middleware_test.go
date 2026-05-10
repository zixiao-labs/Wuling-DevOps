package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dummyHandler(t *testing.T, captured **Identity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if captured != nil {
			id, _ := IdentityFromContext(r.Context())
			*captured = id
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeErrBody(t *testing.T, body string) errBody {
	t.Helper()
	var e errBody
	require.NoError(t, json.NewDecoder(strings.NewReader(body)).Decode(&e))
	return e
}

func TestMiddleware_NoHeader_Required(t *testing.T) {
	v := NewVerifier(testJWTConfig(time.Minute))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	Middleware(v, false)(dummyHandler(t, nil)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, `Bearer realm="wuling"`, rr.Header().Get("WWW-Authenticate"))
	body := decodeErrBody(t, rr.Body.String())
	assert.Equal(t, "unauthorized", body.Error.Code)
	assert.Contains(t, body.Error.Message, "Authorization")
}

func TestMiddleware_NoHeader_Optional(t *testing.T) {
	v := NewVerifier(testJWTConfig(time.Minute))
	var captured *Identity
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	Middleware(v, true)(dummyHandler(t, &captured)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.Nil(t, captured, "optional pass-through must not attach an identity")
}

func TestMiddleware_NotBearer(t *testing.T) {
	v := NewVerifier(testJWTConfig(time.Minute))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Basic Zm9vOmJhcg==")
	Middleware(v, false)(dummyHandler(t, nil)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, decodeErrBody(t, rr.Body.String()).Error.Message, "Bearer")
}

func TestMiddleware_InvalidToken(t *testing.T) {
	v := NewVerifier(testJWTConfig(time.Minute))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer not.a.real.jwt")
	Middleware(v, false)(dummyHandler(t, nil)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, "unauthorized", decodeErrBody(t, rr.Body.String()).Error.Code)
}

func TestMiddleware_ExpiredToken(t *testing.T) {
	cfg := testJWTConfig(-time.Second)
	tok, _, err := NewIssuer(cfg).Issue(uuid.New(), "u")
	require.NoError(t, err)

	verifier := NewVerifier(testJWTConfig(time.Minute))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	Middleware(verifier, false)(dummyHandler(t, nil)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestMiddleware_Valid(t *testing.T) {
	cfg := testJWTConfig(time.Minute)
	uid := uuid.New()
	tok, _, err := NewIssuer(cfg).Issue(uid, "alice")
	require.NoError(t, err)

	var captured *Identity
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	Middleware(NewVerifier(cfg), false)(dummyHandler(t, &captured)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	require.NotNil(t, captured)
	assert.Equal(t, uid, captured.UserID)
	assert.Equal(t, "alice", captured.Username)
	assert.Equal(t, IdentitySourceJWT, captured.Source)
}

func TestRequireIdentity_Missing(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	_, err := RequireIdentity(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication required")
}

func TestRequireIdentity_Present(t *testing.T) {
	uid := uuid.New()
	want := &Identity{UserID: uid, Username: "u", Source: IdentitySourceJWT}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req = req.WithContext(WithIdentity(req.Context(), want))
	got, err := RequireIdentity(req)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestWithIdentity_RoundTrip(t *testing.T) {
	want := &Identity{UserID: uuid.New(), Username: "x", Source: IdentitySourcePAT}
	ctx := WithIdentity(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil).Context(), want)
	got, ok := IdentityFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, want, got)

	_, ok = IdentityFromContext(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil).Context())
	assert.False(t, ok, "fresh ctx has no identity")
}
