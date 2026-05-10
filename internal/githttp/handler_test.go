package githttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// ---------- pure helpers ----------

func TestPktLine(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hi", "0006hi"},
		{"", "0004"},
		{"# service=git-upload-pack\n", "001e# service=git-upload-pack\n"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, pktLine(c.in))
		})
	}
}

func TestHasScope(t *testing.T) {
	assert.True(t, hasScope([]string{"repo:read", "repo:write"}, "repo:write"))
	assert.False(t, hasScope([]string{"repo:read"}, "repo:write"))
	assert.False(t, hasScope(nil, "repo:write"))
}

func TestEscape(t *testing.T) {
	assert.Equal(t, `a\"b\\c\nd\re`, escape("a\"b\\c\nd\re"))
	assert.Equal(t, "plain", escape("plain"))
}

// ---------- authenticate parsing ----------

type fakePATResolver struct {
	called   bool
	username string
	raw      string
	id       *auth.Identity
	err      error
}

func (f *fakePATResolver) ResolvePAT(_ context.Context, username, raw string) (*auth.Identity, error) {
	f.called = true
	f.username = username
	f.raw = raw
	return f.id, f.err
}

type fakePasswordResolver struct {
	called   bool
	username string
	password string
	id       *auth.Identity
	err      error
}

func (f *fakePasswordResolver) ResolvePassword(_ context.Context, username, password string) (*auth.Identity, error) {
	f.called = true
	f.username = username
	f.password = password
	return f.id, f.err
}

func basicAuthReq(t *testing.T, user, pass string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	return req
}

func TestAuthenticate_NoHeader(t *testing.T) {
	h := &Handler{}
	_, err := h.authenticate(httptest.NewRequest(http.MethodGet, "/", nil))
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeUnauthorized, e.Code)
}

func TestAuthenticate_NotBasic(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer something")
	_, err := h.authenticate(req)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeUnauthorized, apperr.As(err).Code)
}

func TestAuthenticate_BadBase64(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic !!!not-base64")
	_, err := h.authenticate(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid auth header")
}

func TestAuthenticate_NoColon(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// "userpass" base64 = "dXNlcnBhc3M=" — no colon inside.
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("userpass")))
	_, err := h.authenticate(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid auth header")
}

func TestAuthenticate_EmptyCreds(t *testing.T) {
	h := &Handler{}
	_, err := h.authenticate(basicAuthReq(t, "", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing credentials")
}

func TestAuthenticate_PATPath(t *testing.T) {
	wantID := &auth.Identity{UserID: uuid.New(), Username: "alice", Source: auth.IdentitySourcePAT}
	pat := &fakePATResolver{id: wantID}
	pw := &fakePasswordResolver{}
	h := &Handler{PATReslv: pat, PWReslv: pw}

	got, err := h.authenticate(basicAuthReq(t, "alice", auth.AccessTokenPrefix+"abcd"))
	require.NoError(t, err)
	assert.Equal(t, wantID, got)
	assert.True(t, pat.called)
	assert.False(t, pw.called, "password resolver must not be called for wlpat_ tokens")
	assert.Equal(t, "alice", pat.username)
}

func TestAuthenticate_PasswordPath(t *testing.T) {
	wantID := &auth.Identity{UserID: uuid.New(), Username: "bob", Source: auth.IdentitySourcePassword}
	pat := &fakePATResolver{}
	pw := &fakePasswordResolver{id: wantID}
	h := &Handler{PATReslv: pat, PWReslv: pw}

	got, err := h.authenticate(basicAuthReq(t, "bob", "letmein"))
	require.NoError(t, err)
	assert.Equal(t, wantID, got)
	assert.False(t, pat.called)
	assert.True(t, pw.called)
	assert.Equal(t, "letmein", pw.password)
}

// ---------- subprocess plumbing via fakegit.sh ----------

// fakegitFixture wires a Handler whose GitBinary points at our recording
// shell script. Callers prime FakeStdoutPayload before issuing the request,
// then read FakeArgs / FakeStdin to verify what hit the subprocess.
type fakegitFixture struct {
	dir            string
	argsFile       string
	stdinFile      string
	stdoutPayload  string
	scriptAbs      string
}

func newFakegitFixture(t *testing.T) *fakegitFixture {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	scriptAbs := filepath.Join(wd, "testdata", "fakegit.sh")
	st, err := os.Stat(scriptAbs)
	require.NoError(t, err, "fakegit.sh missing — did chmod survive checkout?")
	require.True(t, st.Mode()&0o111 != 0, "fakegit.sh must be executable")

	dir := t.TempDir()
	return &fakegitFixture{
		dir:       dir,
		argsFile:  filepath.Join(dir, "args"),
		stdinFile: filepath.Join(dir, "stdin"),
		scriptAbs: scriptAbs,
	}
}

// install puts the fixture's env-vars into the os.Environ for the test scope.
// gitCommand passes only PATH and LANG to the child, so we need our env to be
// readable via os.Getenv inside the test process — which then propagates via
// the explicit env list... wait, no, gitCommand sets cmd.Env explicitly
// stripping everything except PATH and LANG. So we need to monkey-patch.
//
// Solution: use t.Setenv so the test process has these vars. Then for the
// child, supplement gitCommand by overriding GitBinary to a wrapper script
// that re-injects the env. Simpler: use a per-test wrapper script written
// fresh in the test temp dir that invokes fakegit.sh with the right env.
func (f *fakegitFixture) wrapperScript(t *testing.T) string {
	t.Helper()
	wrapper := filepath.Join(f.dir, "git-wrapper.sh")
	body := strings.Join([]string{
		"#!/usr/bin/env bash",
		"export FAKEGIT_ARGS_FILE=" + shquote(f.argsFile),
		"export FAKEGIT_STDIN_FILE=" + shquote(f.stdinFile),
		"export FAKEGIT_STDOUT_PAYLOAD=" + shquote(f.stdoutPayload),
		"exec " + shquote(f.scriptAbs) + ` "$@"`,
		"",
	}, "\n")
	require.NoError(t, os.WriteFile(wrapper, []byte(body), 0o755))
	return wrapper
}

func (f *fakegitFixture) readArgs(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.argsFile)
	require.NoError(t, err, "fakegit was not invoked: argv file missing")
	return strings.TrimRight(string(b), "\n")
}

func (f *fakegitFixture) readStdin(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(f.stdinFile)
	require.NoError(t, err, "fakegit stdin file missing")
	return b
}

// shquote single-quotes a string for safe inclusion in a bash command.
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fixtureSeed inserts a user, project, repo, and PAT for tests that drive
// the smart-HTTP routes through a real *userstore.Store.
type fixtureSeed struct {
	pool       *userstore.Store
	user       uuid.UUID
	username   string
	patRaw     string
	orgSlug    string
	projSlug   string
	repoSlug   string
	repoVis    string
	repoScopes []string
}

func seedFixture(t *testing.T, store *userstore.Store, visibility string, scopes []string) *fixtureSeed {
	t.Helper()
	ctx := context.Background()
	username := "tester" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")

	hash, err := auth.HashPassword("not-used")
	require.NoError(t, err)
	user, org, err := store.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        username + "@example.com",
		PasswordHash: hash,
	})
	require.NoError(t, err)

	proj, err := store.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID,
		Slug:  "proj-" + username,
	})
	require.NoError(t, err)

	repo, err := store.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID:  proj.ID,
		Slug:       "repo-" + username,
		Visibility: visibility,
	})
	require.NoError(t, err)

	rawPAT, hashedPAT, err := auth.NewAccessToken()
	require.NoError(t, err)
	_, err = store.CreatePAT(ctx, userstore.CreatePATParams{
		UserID: user.ID,
		Name:   "test",
		Hash:   hashedPAT,
		Scopes: scopes,
	})
	require.NoError(t, err)

	_ = repo // referenced via slug below; avoid unused-var in case tests change shape
	return &fixtureSeed{
		pool:       store,
		user:       user.ID,
		username:   user.Username,
		patRaw:     rawPAT,
		orgSlug:    org.Slug,
		projSlug:   proj.Slug,
		repoSlug:   repo.Slug,
		repoVis:    visibility,
		repoScopes: scopes,
	}
}

// realPATResolver is a minimal PAT resolver for integration tests. Mirrors
// internal/authhttp.PATResolver.ResolvePAT but lives here to avoid the
// cross-package import in tests.
type realPATResolver struct{ Store *userstore.Store }

func (r *realPATResolver) ResolvePAT(ctx context.Context, username, raw string) (*auth.Identity, error) {
	if !strings.HasPrefix(raw, auth.AccessTokenPrefix) {
		return nil, apperr.Unauthorized("invalid token")
	}
	user, err := r.Store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	rows, err := r.Store.ListPATAuthRowsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		ok, _ := auth.VerifyAccessToken(raw, row.Hash)
		if ok {
			return &auth.Identity{
				UserID:   user.ID,
				Username: user.Username,
				Source:   auth.IdentitySourcePAT,
				Scopes:   row.Scopes,
			}, nil
		}
	}
	return nil, apperr.Unauthorized("invalid token")
}

type denyPasswordResolver struct{}

func (denyPasswordResolver) ResolvePassword(_ context.Context, _, _ string) (*auth.Identity, error) {
	return nil, apperr.Unauthorized("password auth disabled in test")
}

// newSubprocessHandler builds a Handler wired to the test DB and a fakegit
// wrapper. Returns the handler, a chi.Mux mounting it, and the fixture.
func newSubprocessHandler(t *testing.T, visibility string, patScopes []string) (*Handler, http.Handler, *fixtureSeed, *fakegitFixture) {
	t.Helper()
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)

	seed := seedFixture(t, store, visibility, patScopes)

	fix := newFakegitFixture(t)
	wrapper := fix.wrapperScript(t)

	h := &Handler{
		Store:     store,
		Layout:    repostore.New(t.TempDir()),
		PWReslv:   denyPasswordResolver{},
		PATReslv:  &realPATResolver{Store: store},
		GitBinary: wrapper,
	}
	r := chi.NewRouter()
	h.Mount(r)
	return h, r, seed, fix
}

// ---------- info/refs ----------

func TestInfoRefs_BadService(t *testing.T) {
	_, mux, seed, _ := newSubprocessHandler(t, "public", []string{"repo:read"})
	rr := httptest.NewRecorder()
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/info/refs?service=git-foo"
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestInfoRefs_UploadPack_Public_Anonymous(t *testing.T) {
	_, mux, seed, fix := newSubprocessHandler(t, "public", []string{"repo:read"})
	fix.stdoutPayload = "REFS_ADVERTISEMENT"
	// Re-write wrapper now that we have a payload.
	fix.wrapperScript(t)

	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/info/refs?service=git-upload-pack"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))

	assert.Equal(t, http.StatusOK, rr.Code, "anonymous read of public repo allowed")
	assert.Equal(t, "application/x-git-upload-pack-advertisement", rr.Header().Get("Content-Type"))
	body := rr.Body.String()
	assert.Contains(t, body, "# service=git-upload-pack")
	assert.Contains(t, body, "REFS_ADVERTISEMENT", "payload from fakegit must reach client")

	args := fix.readArgs(t)
	assert.Contains(t, args, "upload-pack --stateless-rpc --advertise-refs", "expected git subcommand + flags")
	assert.Contains(t, args, ".git", "repo path must end in .git")
}

func TestInfoRefs_ReceivePack_Anonymous_Unauthorized(t *testing.T) {
	_, mux, seed, _ := newSubprocessHandler(t, "public", []string{"repo:read"})
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/info/refs?service=git-receive-pack"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, `Basic realm="wuling-git", charset="UTF-8"`, rr.Header().Get("WWW-Authenticate"))
}

func TestInfoRefs_ReceivePack_PATWithoutWriteScope_Forbidden(t *testing.T) {
	_, mux, seed, _ := newSubprocessHandler(t, "private", []string{"repo:read"})
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/info/refs?service=git-receive-pack"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(seed.username+":"+seed.patRaw)))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestInfoRefs_ReceivePack_PATWithWriteScope_OK(t *testing.T) {
	_, mux, seed, fix := newSubprocessHandler(t, "private", []string{"repo:read", "repo:write"})
	fix.stdoutPayload = "REFS_ADVERTISEMENT"
	fix.wrapperScript(t)

	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/info/refs?service=git-receive-pack"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(seed.username+":"+seed.patRaw)))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	args := fix.readArgs(t)
	assert.Contains(t, args, "receive-pack --stateless-rpc --advertise-refs")
}

// ---------- service-pack POST endpoints ----------

func TestUploadPack_BadContentType(t *testing.T) {
	_, mux, seed, _ := newSubprocessHandler(t, "public", []string{"repo:read"})
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/git-upload-pack"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUploadPack_PassesStdin(t *testing.T) {
	_, mux, seed, fix := newSubprocessHandler(t, "public", []string{"repo:read"})
	fix.stdoutPayload = "PACK_RESULT"
	fix.wrapperScript(t)

	wantBody := "want 1234567890abcdef\n0000"
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/git-upload-pack"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(wantBody))
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/x-git-upload-pack-result", rr.Header().Get("Content-Type"))
	assert.Equal(t, "PACK_RESULT", rr.Body.String())
	assert.Equal(t, wantBody, string(fix.readStdin(t)), "stdin must reach fakegit byte-for-byte")
	args := fix.readArgs(t)
	assert.Contains(t, args, "upload-pack --stateless-rpc")
	assert.NotContains(t, args, "--advertise-refs", "POST path must not advertise-refs")
}

func TestUploadPack_GzipBody(t *testing.T) {
	_, mux, seed, fix := newSubprocessHandler(t, "public", []string{"repo:read"})
	fix.stdoutPayload = "OK"
	fix.wrapperScript(t)

	// Gzip the body.
	var gzbuf bytes.Buffer
	zw := gzip.NewWriter(&gzbuf)
	wantBody := "want 1234567890abcdef\n0000"
	_, err := zw.Write([]byte(wantBody))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/git-upload-pack"
	req := httptest.NewRequest(http.MethodPost, url, &gzbuf)
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, wantBody, string(fix.readStdin(t)), "gzip body must be decompressed before reaching fakegit")
}

func TestUploadPack_GzipBody_Bad(t *testing.T) {
	_, mux, seed, _ := newSubprocessHandler(t, "public", []string{"repo:read"})
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/git-upload-pack"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader("not gzip"))
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestReceivePack_MarksNotEmpty(t *testing.T) {
	_, mux, seed, fix := newSubprocessHandler(t, "private", []string{"repo:read", "repo:write"})
	fix.stdoutPayload = "OK"
	fix.wrapperScript(t)

	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/git-receive-pack"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader("0000"))
	req.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(seed.username+":"+seed.patRaw)))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, body(rr))

	// Verify is_empty flipped to false.
	repo, _, _, err := seed.pool.ResolveRepoPath(context.Background(), seed.orgSlug, seed.projSlug, seed.repoSlug)
	require.NoError(t, err)
	assert.False(t, repo.IsEmpty, "successful push must clear is_empty")
}

// ---------- repo resolution edge cases ----------

func TestResolveRepo_PrivateRead_NoAuth_Unauthorized(t *testing.T) {
	_, mux, seed, _ := newSubprocessHandler(t, "private", []string{"repo:read"})
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + ".git/info/refs?service=git-upload-pack"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestResolveRepo_RepoSlugWithoutDotGit(t *testing.T) {
	_, mux, seed, fix := newSubprocessHandler(t, "public", []string{"repo:read"})
	fix.stdoutPayload = "REFS"
	fix.wrapperScript(t)

	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/" + seed.repoSlug + "/info/refs?service=git-upload-pack"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
	assert.Equal(t, http.StatusOK, rr.Code, "repo slug without .git suffix should still resolve")
}

func TestResolveRepo_NotFound(t *testing.T) {
	_, mux, seed, _ := newSubprocessHandler(t, "public", []string{"repo:read"})
	url := "/" + seed.orgSlug + "/" + seed.projSlug + "/missing-repo.git/info/refs?service=git-upload-pack"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// ---------- helpers ----------

// body extracts the JSON error message from a response for assertion failure
// messages, falling back to the raw body if it isn't an error envelope.
func body(rr *httptest.ResponseRecorder) string {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(&env); err == nil && env.Error.Code != "" {
		return env.Error.Code + ": " + env.Error.Message
	}
	return rr.Body.String()
}

// errors guard so go vet doesn't complain about an unused import in some
// build configurations (io is referenced via io.ReadAll only in some tests).
var _ = errors.New
var _ io.Reader = (*bytes.Reader)(nil)
