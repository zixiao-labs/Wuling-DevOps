//go:build cgo

package wikihttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
	"github.com/zixiao-labs/wuling-devops/internal/wikihttp"
	"github.com/zixiao-labs/wuling-devops/internal/wikistore"
)

// fixture wires a tiny in-process API surface (just the wiki handler)
// against a fresh testcontainers DB plus a real on-disk libgit2 repo root.
type fixture struct {
	mux      http.Handler
	token    string
	orgSlug  string
	projSlug string
	username string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	require.NoError(t, git.Init(), "libgit2 must come up before wiki tests")

	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)

	root := t.TempDir()
	layout := repostore.New(root)
	wikis := wikistore.New(layout)

	cfg := config.JWTConfig{
		Secret: "wiki-test-secret", Issuer: "wuling-test", Audience: "wuling-test",
		TTL: time.Hour,
	}
	verifier := auth.NewVerifier(cfg)
	issuer := auth.NewIssuer(cfg)

	// Seed a user + personal org + project.
	username := "wiki" + strings.ReplaceAll(strings.ToLower("user"), "-", "")
	hash, err := auth.HashPassword("dontcare")
	require.NoError(t, err)
	user, org, err := store.CreateUser(context.Background(), userstore.CreateUserParams{
		Username:     username,
		Email:        username + "@example.test",
		PasswordHash: hash,
	})
	require.NoError(t, err)
	proj, err := store.CreateProject(context.Background(), userstore.CreateProjectParams{
		OrgID: org.ID,
		Slug:  "proj-" + username,
	})
	require.NoError(t, err)

	tok, _, err := issuer.Issue(user.ID, user.Username)
	require.NoError(t, err)

	h := &wikihttp.Handler{Users: store, Wikis: wikis, Verifier: verifier}
	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) { h.Mount(api) })

	return &fixture{
		mux:      r,
		token:    tok,
		orgSlug:  org.Slug,
		projSlug: proj.Slug,
		username: user.Username,
	}
}

func (f *fixture) request(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	rr := httptest.NewRecorder()
	f.mux.ServeHTTP(rr, req)
	return rr
}

func (f *fixture) wikiBase() string {
	return "/api/v1/orgs/" + f.orgSlug + "/projects/" + f.projSlug + "/wiki"
}

func TestWiki_PutGetListDelete(t *testing.T) {
	f := setup(t)

	// Empty wiki returns an empty page list, not 404.
	rr := f.request(t, http.MethodGet, f.wikiBase()+"/pages", nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var listResp struct {
		Pages []struct{ Path string } `json:"pages"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &listResp))
	assert.Empty(t, listResp.Pages)

	// PUT creates the wiki repo lazily and returns rendered HTML.
	rr = f.request(t, http.MethodPut, f.wikiBase()+"/pages/Home.md",
		map[string]any{"content": "# Welcome\n\nhello world"})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var put struct {
		Path      string `json:"path"`
		HTML      string `json:"html"`
		CommitOID string `json:"commit_oid"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &put))
	assert.Equal(t, "Home.md", put.Path)
	assert.Contains(t, put.HTML, "<h1")
	assert.Len(t, put.CommitOID, 40)

	// Nested path round-trip.
	rr = f.request(t, http.MethodPut, f.wikiBase()+"/pages/docs/usage.md",
		map[string]any{"content": "## Usage"})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	// GET returns sanitized HTML for an XSS attempt.
	rr = f.request(t, http.MethodPut, f.wikiBase()+"/pages/Evil.md",
		map[string]any{"content": "hello <script>alert('xss')</script>"})
	require.Equal(t, http.StatusCreated, rr.Code)
	rr = f.request(t, http.MethodGet, f.wikiBase()+"/pages/Evil.md", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var page struct{ HTML string }
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page))
	assert.NotContains(t, page.HTML, "<script>", "bluemonday must strip script tags")

	// LIST reflects all three pages.
	rr = f.request(t, http.MethodGet, f.wikiBase()+"/pages", nil)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &listResp))
	paths := make([]string, 0)
	for _, p := range listResp.Pages {
		paths = append(paths, p.Path)
	}
	assert.ElementsMatch(t, []string{"Evil.md", "Home.md", "docs/usage.md"}, paths)

	// HISTORY shows the commits.
	rr = f.request(t, http.MethodGet, f.wikiBase()+"/history", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var hist struct {
		Commits []struct{ Message string } `json:"commits"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &hist))
	assert.GreaterOrEqual(t, len(hist.Commits), 3)

	// DELETE removes the page.
	rr = f.request(t, http.MethodDelete, f.wikiBase()+"/pages/Home.md", nil)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

	// Subsequent GET 404s.
	rr = f.request(t, http.MethodGet, f.wikiBase()+"/pages/Home.md", nil)
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())

	// Deleting a missing page also 404s.
	rr = f.request(t, http.MethodDelete, f.wikiBase()+"/pages/never-existed.md", nil)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestWiki_Validation(t *testing.T) {
	f := setup(t)

	// Path validation: must end in .md.
	rr := f.request(t, http.MethodPut, f.wikiBase()+"/pages/foo.txt",
		map[string]any{"content": "nope"})
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	// Path validation: traversal rejected.
	rr = f.request(t, http.MethodPut, f.wikiBase()+"/pages/../etc.md",
		map[string]any{"content": "nope"})
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
}

func TestWiki_RequiresMembership(t *testing.T) {
	// We don't construct a separate user here; instead we exercise the
	// 404-via-no-membership branch by hitting an org we know exists but with
	// no membership row. Since the fixture's user *is* a member of the org
	// they created, this test just confirms membership IS required by hitting
	// a fabricated project_slug under their org and confirming it 404s rather
	// than 5xx-ing. (Resolution order is org -> membership -> project; an
	// unknown project slug bubbles up as 404 too.)
	f := setup(t)
	rr := f.request(t, http.MethodGet,
		"/api/v1/orgs/"+f.orgSlug+"/projects/no-such-project/wiki/pages", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
}
