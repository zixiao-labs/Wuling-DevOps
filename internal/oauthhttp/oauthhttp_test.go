// Happy-path integration tests for the OAuth provider HTTP surface.
// Each test boots a Postgres via testcontainers (dbtest), wires the full
// handler, and exercises the protocol via httptest.
package oauthhttp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/oauthhttp"
	"github.com/zixiao-labs/wuling-devops/internal/oauthstore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"

	"github.com/go-chi/chi/v5"
)

const hmacSecret = "test-hmac-secret"

// setup boots the test DB and returns a fully-wired handler + mux. Each test
// gets its own fixture, with deterministic Now() returning the call origin.
func setup(t *testing.T) (*oauthhttp.Handler, http.Handler, *userstore.Store, *oauthstore.Store) {
	t.Helper()
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)

	users := userstore.New(pool)
	store := oauthstore.New(pool)
	hasher := auth.NewHMACHasher(hmacSecret)
	issuer := auth.NewIssuer(config.JWTConfig{
		Secret:   "jwt-secret",
		Issuer:   "test",
		Audience: "test-aud",
		TTL:      time.Hour,
	})

	h := oauthhttp.New(oauthhttp.Handler{
		OAuth:  store,
		Users:  users,
		Issuer: issuer,
		Hasher: hasher,
		Cfg: config.OAuthConfig{
			FrontendBaseURL:    "/",
			DesktopClientID:    "wuling-desktop",
			ProviderHMACSecret: hmacSecret,
			PublicBaseURL:      "http://test",
		},
		DevicePollMin: 10 * time.Millisecond, // fast tests; spec default is 5s
	})

	r := chi.NewRouter()
	r.Route("/api/v1/oauth", func(or chi.Router) {
		h.Mount(or)
		or.Group(func(p chi.Router) {
			// Test middleware: read X-Test-User-ID and stuff an identity.
			p.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					uid := r.Header.Get("X-Test-User-ID")
					if uid == "" {
						http.Error(w, "no test user", http.StatusUnauthorized)
						return
					}
					id := &auth.Identity{
						UserID:   uuid.MustParse(uid),
						Username: "tester",
						Source:   auth.IdentitySourceJWT,
					}
					r = r.WithContext(auth.WithIdentity(r.Context(), id))
					next.ServeHTTP(w, r)
				})
			})
			h.MountAuthed(p)
		})
	})
	r.Get("/.well-known/wuling-clients", h.WellKnownHandler())
	return h, r, users, store
}

func makeUser(t *testing.T, users *userstore.Store) uuid.UUID {
	t.Helper()
	u, _, err := users.CreateUser(context.Background(), userstore.CreateUserParams{
		Username:       "alice",
		Email:          "alice@example.com",
		DisplayName:    "Alice",
		ApprovalStatus: "approved",
	})
	require.NoError(t, err)
	return u.ID
}

func seedFirstParty(t *testing.T, store *oauthstore.Store) *oauthstore.Client {
	t.Helper()
	c, err := store.UpsertFirstPartyClient(context.Background(), oauthstore.CreateClientParams{
		ClientID:      "wuling-desktop",
		Name:          "Wuling Desktop",
		RedirectURIs:  []string{"http://127.0.0.1"},
		DefaultScopes: []string{"user:read", "git:read", "git:write"},
	})
	require.NoError(t, err)
	return c
}

// --- well-known ---

func TestWellKnown(t *testing.T) {
	_, mux, _, store := setup(t)
	seedFirstParty(t, store)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/.well-known/wuling-clients", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	var doc map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&doc))
	assert.Equal(t, "wuling-desktop", doc["desktop_official_client_id"])
	assert.Equal(t, "http://test/api/v1/oauth/token", doc["token_endpoint"])
	assert.Equal(t, "http://test/api/v1/oauth/device_authorization", doc["device_authorization_endpoint"])
}

// --- device flow happy path ---

func TestDeviceFlow_HappyPath(t *testing.T) {
	_, mux, users, store := setup(t)
	client := seedFirstParty(t, store)
	uid := makeUser(t, users)

	// 1. Device authorizes.
	form := url.Values{}
	form.Set("client_id", client.ClientID)
	form.Set("scope", "user:read")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/device_authorization", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var devResp struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		Interval   int    `json:"interval"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&devResp))
	require.NotEmpty(t, devResp.DeviceCode)
	require.NotEmpty(t, devResp.UserCode)

	// 2. Polling /token before user approves -> authorization_pending.
	tokenReq := func(extras url.Values) (int, map[string]any) {
		form := url.Values{}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		form.Set("device_code", devResp.DeviceCode)
		form.Set("client_id", client.ClientID)
		for k, v := range extras {
			form[k] = v
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/token",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		body, _ := io.ReadAll(rr.Body)
		out := map[string]any{}
		_ = json.Unmarshal(body, &out)
		return rr.Code, out
	}
	code, body := tokenReq(nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "authorization_pending", body["error"])

	// 3. User approves via the consent endpoint.
	approveBody := []byte(`{"user_code": "` + devResp.UserCode + `"}`)
	areq := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/device/approve", strings.NewReader(string(approveBody)))
	areq.Header.Set("Content-Type", "application/json")
	areq.Header.Set("X-Test-User-ID", uid.String())
	arr := httptest.NewRecorder()
	mux.ServeHTTP(arr, areq)
	require.Equal(t, http.StatusOK, arr.Code, "approve body=%s", arr.Body.String())

	// 4. Device re-polls. With DevicePollMin = 10ms we sleep enough to dodge
	// slow_down. Try a couple times in case the wall-clock didn't tick.
	for i := 0; i < 5; i++ {
		time.Sleep(20 * time.Millisecond)
		code, body = tokenReq(nil)
		if code == http.StatusOK {
			break
		}
	}
	require.Equal(t, http.StatusOK, code, "expected token issuance, got %d %v", code, body)
	require.NotEmpty(t, body["access_token"])
	assert.Equal(t, "Bearer", body["token_type"])
	assert.NotEmpty(t, body["refresh_token"])
	scope, _ := body["scope"].(string)
	assert.Contains(t, scope, "user:read")
}

// --- OAT resolver round-trip ---

func TestResolveOAT(t *testing.T) {
	h, _, users, store := setup(t)
	client := seedFirstParty(t, store)
	uid := makeUser(t, users)

	// Mint a token directly via the store.
	hasher := auth.NewHMACHasher(hmacSecret)
	raw, hash, err := auth.NewOAT(hasher)
	require.NoError(t, err)
	_, err = store.CreateAccessToken(context.Background(), oauthstore.CreateAccessTokenParams{
		UserID:    uid,
		ClientID:  client.ID,
		TokenHash: hash,
		Scopes:    []string{"git:read", "git:write"},
		TokenTTL:  time.Hour,
	})
	require.NoError(t, err)

	id, err := h.ResolveOAT(context.Background(), raw)
	require.NoError(t, err)
	assert.Equal(t, uid, id.UserID)
	assert.Equal(t, "alice", id.Username)
	assert.Equal(t, auth.IdentitySourceOAT, id.Source)
	assert.ElementsMatch(t, []string{"git:read", "git:write"}, id.Scopes)
	assert.Equal(t, client.ID, id.ClientID)
}

// --- scope check ---

func TestResolveOAT_RevokedTokenRejected(t *testing.T) {
	h, _, users, store := setup(t)
	client := seedFirstParty(t, store)
	uid := makeUser(t, users)

	hasher := auth.NewHMACHasher(hmacSecret)
	raw, hash, err := auth.NewOAT(hasher)
	require.NoError(t, err)
	row, err := store.CreateAccessToken(context.Background(), oauthstore.CreateAccessTokenParams{
		UserID:    uid,
		ClientID:  client.ID,
		TokenHash: hash,
		Scopes:    []string{"user:read"},
		TokenTTL:  time.Hour,
	})
	require.NoError(t, err)
	require.NoError(t, store.RevokeAccessToken(context.Background(), row.ID))

	_, err = h.ResolveOAT(context.Background(), raw)
	require.Error(t, err)
}
