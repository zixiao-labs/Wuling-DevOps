//go:build cgo

package runnerhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerhttp"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

const validRunnerConfig = `version: 1
tiers:
  low: {cpu: 1}
pools:
  - name: aws-low
    provider: aws
    tier: low
    aws:
      region: us-west-2
      credentials_secret: AWS_CREDS
`

const invalidRunnerConfig = `version: 1
tiers:
  low: {cpu: 1}
pools:
  - name: bad
    provider: aws
    tier: low
    aliyun:
      region: cn-hangzhou
      credentials_secret: C
`

type runnerFixture struct {
	mux              http.Handler
	ownerToken       string
	developerToken   string
	orgSlug          string
	orgConfig        *orgconfig.Store
}

func setupRunnerFixture(t *testing.T) *runnerFixture {
	t.Helper()
	require.NoError(t, git.Init())

	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	users := userstore.New(pool)
	layout := repostore.New(t.TempDir())
	orgCfg := orgconfig.New(users, layout, "config", "config")
	runners := runnerstore.New(pool)
	pipelines := pipelinestore.New(pool, t.TempDir())

	jwtCfg := config.JWTConfig{
		Secret: "runner-config-test", Issuer: "wuling-test", Audience: "wuling-test",
		TTL: time.Hour,
	}
	verifier := auth.NewVerifier(jwtCfg)
	issuer := auth.NewIssuer(jwtCfg)

	owner, org, err := users.CreateUser(context.Background(), userstore.CreateUserParams{
		Username: "runner-owner", Email: "runner-owner@example.test",
	})
	require.NoError(t, err)
	developer, _, err := users.CreateUser(context.Background(), userstore.CreateUserParams{
		Username: "runner-dev", Email: "runner-dev@example.test",
	})
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `
		INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'developer')
	`, org.ID, developer.ID)
	require.NoError(t, err)

	ownerTok, _, err := issuer.Issue(owner.ID, owner.Username)
	require.NoError(t, err)
	devTok, _, err := issuer.Issue(developer.ID, developer.Username)
	require.NoError(t, err)

	h := &runnerhttp.Handler{
		Users: users, Runners: runners, Pipelines: pipelines,
		Verifier: verifier, OrgConfig: orgCfg,
		RegistrationTTL: time.Hour,
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) { h.Mount(api) })

	return &runnerFixture{
		mux:            r,
		ownerToken:     ownerTok,
		developerToken: devTok,
		orgSlug:        org.Slug,
		orgConfig:      orgCfg,
	}
}

func (f *runnerFixture) request(t *testing.T, token, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var payload map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	}
	return rec, payload
}

func TestPutRunnerConfig_InvalidParse_NoCommit(t *testing.T) {
	f := setupRunnerFixture(t)
	base := "/api/v1/orgs/" + f.orgSlug + "/runner-config"
	empty := ""

	rec, payload := f.request(t, f.ownerToken, http.MethodPut, base, map[string]any{
		"content":       invalidRunnerConfig,
		"base_blob_sha": empty,
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	errObj, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "validation", errObj["code"])

	getRec, getPayload := f.request(t, f.ownerToken, http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, getRec.Code)
	assert.Equal(t, false, getPayload["exists"])
}

func TestPutRunnerConfig_Developer_Forbidden(t *testing.T) {
	f := setupRunnerFixture(t)
	base := "/api/v1/orgs/" + f.orgSlug + "/runner-config"
	empty := ""

	rec, payload := f.request(t, f.developerToken, http.MethodPut, base, map[string]any{
		"content":       validRunnerConfig,
		"base_blob_sha": empty,
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	errObj, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "forbidden", errObj["code"])
}

func TestPutRunnerConfig_MissingPrecondition_BadRequest(t *testing.T) {
	f := setupRunnerFixture(t)
	base := "/api/v1/orgs/" + f.orgSlug + "/runner-config"

	rec, payload := f.request(t, f.ownerToken, http.MethodPut, base, map[string]any{
		"content": validRunnerConfig,
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	errObj, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "bad_request", errObj["code"])
}

func TestPutRunnerConfig_Valid_CreatesConfig(t *testing.T) {
	f := setupRunnerFixture(t)
	base := "/api/v1/orgs/" + f.orgSlug + "/runner-config"
	empty := ""

	rec, payload := f.request(t, f.ownerToken, http.MethodPut, base, map[string]any{
		"content":       validRunnerConfig,
		"base_blob_sha": empty,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, payload["exists"])
	assert.Equal(t, true, payload["valid"])
	assert.NotEmpty(t, payload["blob_sha"])
}
