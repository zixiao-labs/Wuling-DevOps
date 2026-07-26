package stage2http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/artifactclient"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/stage2store"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

type handlerFixture struct {
	router         http.Handler
	base           string
	ownerToken     string
	developerToken string
	reporterToken  string
	blobs          *fakeArtifactBlobs
}

type fakeArtifactBlobs struct {
	objects    map[string][]byte
	lastSize   int64
	putKeys    []string
	openKeys   []string
	deleteKeys []string
	openErr    error
}

type readDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (r *readDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	r.deadline = deadline
	return nil
}

func (f *fakeArtifactBlobs) Put(
	_ context.Context,
	key string,
	body io.Reader,
	size int64,
	contentType string,
) (*artifactclient.ObjectInfo, error) {
	f.lastSize = size
	f.putKeys = append(f.putKeys, key)
	if _, exists := f.objects[key]; exists {
		return nil, artifactclient.ErrAlreadyExists
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("upload artifact blob: %w", err)
	}
	f.objects[key] = value
	return &artifactclient.ObjectInfo{
		Key: key, Size: int64(len(value)), ContentType: contentType,
	}, nil
}

func (f *fakeArtifactBlobs) Open(_ context.Context, key string) (*artifactclient.Object, error) {
	f.openKeys = append(f.openKeys, key)
	if f.openErr != nil {
		return nil, f.openErr
	}
	value, exists := f.objects[key]
	if !exists {
		return nil, artifactclient.ErrNotFound
	}
	return &artifactclient.Object{
		ObjectInfo: artifactclient.ObjectInfo{
			Key: key, Size: int64(len(value)), ContentType: "text/plain",
		},
		Body: io.NopCloser(bytes.NewReader(value)),
	}, nil
}

func (f *fakeArtifactBlobs) Delete(_ context.Context, key string) error {
	f.deleteKeys = append(f.deleteKeys, key)
	delete(f.objects, key)
	return nil
}

func newHandlerFixture(t *testing.T) handlerFixture {
	t.Helper()
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ctx := t.Context()
	users := userstore.New(pool)

	owner, _, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "stage2-owner", Email: "stage2-owner@example.com",
	})
	require.NoError(t, err)
	developer, _, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "stage2-developer", Email: "stage2-developer@example.com",
	})
	require.NoError(t, err)
	reporter, _, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "stage2-reporter", Email: "stage2-reporter@example.com",
	})
	require.NoError(t, err)
	org, err := users.CreateOrg(ctx, userstore.CreateOrgParams{
		Slug: "stage2-http", DisplayName: "Stage 2 HTTP", OwnerUserID: owner.ID,
	})
	require.NoError(t, err)
	project, err := users.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID, Slug: "delivery", DisplayName: "Delivery",
	})
	require.NoError(t, err)
	_, err = users.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID: project.ID, Slug: "api", DisplayName: "API",
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO org_members (org_id, user_id, role)
		VALUES ($1, $2, 'developer'), ($1, $3, 'reporter')
	`, org.ID, developer.ID, reporter.ID)
	require.NoError(t, err)

	jwtConfig := config.JWTConfig{
		Secret: "stage2-handler-test-secret", Issuer: "stage2-handler-test",
		Audience: "stage2-handler-test", TTL: time.Hour,
	}
	issuer := auth.NewIssuer(jwtConfig)
	issue := func(id uuid.UUID, username string) string {
		t.Helper()
		token, _, issueErr := issuer.Issue(id, username)
		require.NoError(t, issueErr)
		return token
	}

	blobs := &fakeArtifactBlobs{objects: make(map[string][]byte)}
	h := &Handler{
		Users: users, Stage2: stage2store.New(pool), Verifier: auth.NewVerifier(jwtConfig),
		Artifacts: blobs, MaxUploadBytes: 1 << 20,
	}
	router := chi.NewRouter()
	router.Route("/api/v1", func(api chi.Router) { h.Mount(api) })
	return handlerFixture{
		router:         router,
		base:           "/api/v1/orgs/stage2-http/projects/delivery",
		ownerToken:     issue(owner.ID, owner.Username),
		developerToken: issue(developer.ID, developer.Username),
		reporterToken:  issue(reporter.ID, reporter.Username),
		blobs:          blobs,
	}
}

func TestUploadReadDeadline(t *testing.T) {
	const timeout = 2 * time.Hour
	h := &Handler{UploadReadTimeout: timeout}
	recorder := &readDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	request := httptest.NewRequest(http.MethodPost, "/uploads", nil)
	called := false
	handler := h.withUploadReadDeadline(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	started := time.Now()
	handler.ServeHTTP(recorder, request)

	require.True(t, called)
	require.False(t, recorder.deadline.Before(started.Add(timeout)))
	require.False(t, recorder.deadline.After(time.Now().Add(timeout)))
}

func TestArtifactsConfigurationTest(t *testing.T) {
	fixture := newHandlerFixture(t)
	path := fixture.base + "/artifacts/configuration-test"

	status, payload := requestJSON(
		t, fixture.router, fixture.developerToken, http.MethodPost, path, "",
	)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", payload["status"])
	require.Equal(t, "测试成功", payload["message"])
	checks, ok := payload["checks"].([]any)
	require.True(t, ok)
	require.Len(t, checks, 2)
	for _, raw := range checks {
		check := raw.(map[string]any)
		require.True(t, check["upload_ok"].(bool))
		require.True(t, check["download_ok"].(bool))
		require.True(t, check["delete_ok"].(bool))
	}
	require.Empty(t, fixture.blobs.objects)
	require.Len(t, fixture.blobs.putKeys, 2)
	require.Len(t, fixture.blobs.openKeys, 2)
	require.Len(t, fixture.blobs.deleteKeys, 2)
	require.Contains(t, fixture.blobs.putKeys[0], "/packages/")
	require.Contains(t, fixture.blobs.putKeys[1], "/releases/")

	status, payload = requestJSON(
		t, fixture.router, fixture.reporterToken, http.MethodPost, path, "",
	)
	require.Equal(t, http.StatusForbidden, status)
	requireErrorCode(t, payload, "forbidden")
	require.Len(t, fixture.blobs.putKeys, 2)

	fixture.blobs.openErr = errors.New("synthetic storage read failure")
	status, payload = requestJSON(
		t, fixture.router, fixture.developerToken, http.MethodPost, path, "",
	)
	require.Equal(t, http.StatusServiceUnavailable, status)
	requireErrorCode(t, payload, "unavailable")
	errorBody := payload["error"].(map[string]any)
	details := errorBody["details"].(map[string]any)
	failures := details["failures"].([]any)
	require.Len(t, failures, 2)
	for _, raw := range failures {
		failure := raw.(map[string]any)
		require.Equal(t, "download", failure["operation"])
		require.Equal(t, "synthetic storage read failure", failure["reason"])
	}
	require.Empty(t, fixture.blobs.objects)
}

func requestJSON(t *testing.T, router http.Handler, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	var requestBody *bytes.Reader
	if body == "" {
		requestBody = bytes.NewReader(nil)
	} else {
		requestBody = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, path, requestBody)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	payload := map[string]any{}
	if response.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), response.Body.String())
	}
	return response.Code, payload
}

func requestUpload(
	t *testing.T,
	router http.Handler,
	token, path, version, filename string,
	content []byte,
) (int, map[string]any) {
	t.Helper()
	return requestUploadWithMode(t, router, token, path, version, filename, content, false)
}

func requestChunkedUpload(
	t *testing.T,
	router http.Handler,
	token, path, version, filename string,
	content []byte,
) (int, map[string]any) {
	t.Helper()
	return requestUploadWithMode(t, router, token, path, version, filename, content, true)
}

func requestUploadWithMode(
	t *testing.T,
	router http.Handler,
	token, path, version, filename string,
	content []byte,
	chunked bool,
) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = file.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	request := httptest.NewRequest(http.MethodPost, path+"?version="+url.QueryEscape(version), &body)
	if chunked {
		request.ContentLength = -1
		request.TransferEncoding = []string{"chunked"}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	payload := map[string]any{}
	if response.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), response.Body.String())
	}
	return response.Code, payload
}

func responseID(t *testing.T, payload map[string]any) string {
	t.Helper()
	id, ok := payload["id"].(string)
	require.True(t, ok, "response has no string id: %#v", payload)
	return id
}

func requireErrorCode(t *testing.T, payload map[string]any, code string) {
	t.Helper()
	errorBody, ok := payload["error"].(map[string]any)
	require.True(t, ok, "response has no error envelope: %#v", payload)
	require.Equal(t, code, errorBody["code"])
}

func TestMountPermissionsAndValidation(t *testing.T) {
	fixture := newHandlerFixture(t)

	status, payload := requestJSON(t, fixture.router, "", http.MethodGet, fixture.base+"/dashboard", "")
	require.Equal(t, http.StatusUnauthorized, status)
	requireErrorCode(t, payload, "unauthorized")

	status, payload = requestJSON(t, fixture.router, fixture.reporterToken, http.MethodPost, fixture.base+"/work-items", `{"title":"denied"}`)
	require.Equal(t, http.StatusForbidden, status)
	requireErrorCode(t, payload, "forbidden")

	status, payload = requestJSON(t, fixture.router, fixture.developerToken, http.MethodPatch, fixture.base+"/settings", `{"work_item_prefix":"DEV"}`)
	require.Equal(t, http.StatusForbidden, status)
	requireErrorCode(t, payload, "forbidden")

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/iterations", `{"name":"bad","starts_at":"07/01/2026","ends_at":"2026-07-14"}`)
	require.Equal(t, http.StatusBadRequest, status)
	requireErrorCode(t, payload, "validation")

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPatch, fixture.base+"/iterations/not-a-uuid", `{}`)
	require.Equal(t, http.StatusBadRequest, status)
	requireErrorCode(t, payload, "bad_request")

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/work-items?iteration_id=not-a-uuid", "")
	require.Equal(t, http.StatusBadRequest, status)
	requireErrorCode(t, payload, "bad_request")

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/test-plans/not-a-uuid/suites", `{"name":"suite"}`)
	require.Equal(t, http.StatusBadRequest, status)
	requireErrorCode(t, payload, "bad_request")

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/packages", `{"kind":"invalid","name":"package"}`)
	require.Equal(t, http.StatusBadRequest, status)
	requireErrorCode(t, payload, "validation")

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/releases", `{}`)
	require.Equal(t, http.StatusBadRequest, status)
	requireErrorCode(t, payload, "validation")

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/repos/missing/settings", "")
	require.Equal(t, http.StatusNotFound, status)
	requireErrorCode(t, payload, "not_found")
}

func TestMountRepresentativeRouteLifecycle(t *testing.T) {
	fixture := newHandlerFixture(t)

	status, _ := requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/dashboard", "")
	require.Equal(t, http.StatusOK, status)
	status, payload := requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPatch, fixture.base+"/settings", `{"work_item_prefix":"WL"}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "WL", payload["work_item_prefix"])

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/iterations", `{"name":"Sprint 1","starts_at":"2026-07-01","ends_at":"2026-07-14"}`)
	require.Equal(t, http.StatusCreated, status)
	iterationID := responseID(t, payload)
	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPatch, fixture.base+"/iterations/"+iterationID, `{"state":"current"}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "current", payload["state"])
	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPatch, fixture.base+"/iterations/"+iterationID, `{"starts_at":"bad-date"}`)
	require.Equal(t, http.StatusBadRequest, status)
	requireErrorCode(t, payload, "validation")
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/iterations", "")
	require.Equal(t, http.StatusOK, status)

	workItemBody := fmt.Sprintf(`{"title":"Ship Stage 2","type":"user_story","iteration_id":%q}`, iterationID)
	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/work-items", workItemBody)
	require.Equal(t, http.StatusCreated, status)
	number, ok := payload["number"].(float64)
	require.True(t, ok)
	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPatch, fmt.Sprintf("%s/work-items/%.0f", fixture.base, number), `{"state":"active"}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "active", payload["state"])
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/work-items?state=active", "")
	require.Equal(t, http.StatusOK, status)

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/test-plans", `{"name":"Acceptance"}`)
	require.Equal(t, http.StatusCreated, status)
	planID := responseID(t, payload)
	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/test-plans/"+planID+"/suites", `{"name":"API"}`)
	require.Equal(t, http.StatusCreated, status)
	suiteID := responseID(t, payload)
	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/test-plans/"+planID+"/suites/"+suiteID+"/cases", `{"title":"health check","steps":[]}`)
	require.Equal(t, http.StatusCreated, status)
	caseID := responseID(t, payload)
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/test-cases/"+caseID+"/runs", `{"status":"passed"}`)
	require.Equal(t, http.StatusCreated, status)
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/test-plans", "")
	require.Equal(t, http.StatusOK, status)

	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/packages", `{"kind":"npm","name":"@wuling/stage2"}`)
	require.Equal(t, http.StatusCreated, status)
	packageID := responseID(t, payload)
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/packages/"+packageID+"/versions", `{"version":"2.0.0","size_bytes":5}`)
	require.Equal(t, http.StatusCreated, status)
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/packages/"+packageID+"/versions", "")
	require.Equal(t, http.StatusOK, status)
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/packages", "")
	require.Equal(t, http.StatusOK, status)

	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPost, fixture.base+"/releases", `{"tag_name":"v2.0.0","name":"Stage 2","publish":true}`)
	require.Equal(t, http.StatusCreated, status)
	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/releases", "")
	require.Equal(t, http.StatusOK, status)

	status, _ = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodGet, fixture.base+"/repos/api/settings", "")
	require.Equal(t, http.StatusOK, status)
	status, payload = requestJSON(t, fixture.router, fixture.ownerToken, http.MethodPatch, fixture.base+"/repos/api/settings", `{"topics":[" DevOps ","go"],"merge_strategies":["squash"]}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []any{"devops", "go"}, payload["topics"])
}

func TestManualArtifactUploadLifecycle(t *testing.T) {
	fixture := newHandlerFixture(t)
	status, payload := requestJSON(
		t,
		fixture.router,
		fixture.ownerToken,
		http.MethodPost,
		fixture.base+"/packages",
		`{"kind":"cargo","name":"wuling-cli"}`,
	)
	require.Equal(t, http.StatusCreated, status)
	packageID := responseID(t, payload)
	uploadPath := fixture.base + "/packages/" + packageID + "/uploads"

	status, payload = requestUpload(
		t,
		fixture.router,
		fixture.developerToken,
		uploadPath,
		"2.3.0",
		"wuling-cli.tar.gz",
		[]byte("manual artifact"),
	)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, "2.3.0", payload["version"])
	require.Equal(t, float64(len("manual artifact")), payload["size_bytes"])
	require.Len(t, payload["sha256"], 64)
	metadata, ok := payload["metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "wuling-cli.tar.gz", metadata["filename"])
	require.Equal(t, "manual", metadata["source"])
	require.Len(t, fixture.blobs.objects, 1)
	require.Equal(t, int64(-1), fixture.blobs.lastSize, "manual uploads should stream without pre-buffering")

	status, payload = requestUpload(
		t,
		fixture.router,
		fixture.developerToken,
		uploadPath,
		"2.3.0",
		"duplicate.tar.gz",
		[]byte("duplicate"),
	)
	require.Equal(t, http.StatusConflict, status)
	requireErrorCode(t, payload, "already_exists")
	require.Len(t, fixture.blobs.objects, 1)

	status, payload = requestUpload(
		t,
		fixture.router,
		fixture.reporterToken,
		uploadPath,
		"2.3.1",
		"denied.tar.gz",
		[]byte("denied"),
	)
	require.Equal(t, http.StatusForbidden, status)
	requireErrorCode(t, payload, "forbidden")
	require.Len(t, fixture.blobs.objects, 1)

	status, payload = requestUpload(
		t,
		fixture.router,
		fixture.developerToken,
		uploadPath,
		"2.3.2",
		"too-large.tar.gz",
		make([]byte, 2<<20),
	)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
	requireErrorCode(t, payload, "payload_too_large")
	require.Len(t, fixture.blobs.objects, 1)

	status, payload = requestChunkedUpload(
		t,
		fixture.router,
		fixture.developerToken,
		uploadPath,
		"2.3.3",
		"too-large-chunked.tar.gz",
		make([]byte, 2<<20),
	)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
	requireErrorCode(t, payload, "payload_too_large")
	require.Len(t, fixture.blobs.objects, 1)
}
