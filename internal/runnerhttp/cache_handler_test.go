//go:build cgo

package runnerhttp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/artifactclient"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerhttp"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
)

type memoryCacheBlobs struct {
	mu                 sync.Mutex
	objects            map[string][]byte
	putStarted         chan struct{}
	putRelease         chan struct{}
	startOnce          sync.Once
	putErrAfterStore   error
	deleteErr          error
	reportedSizeOffset int64
	afterPut           func(string)
	putCalls           int
}

func newMemoryCacheBlobs() *memoryCacheBlobs {
	return &memoryCacheBlobs{objects: map[string][]byte{}}
}

func (f *memoryCacheBlobs) Put(_ context.Context, key string, body io.Reader, _ int64, contentType string) (*artifactclient.ObjectInfo, error) {
	if f.putStarted != nil {
		f.startOnce.Do(func() { close(f.putStarted) })
		<-f.putRelease
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.putCalls++
	if _, exists := f.objects[key]; exists {
		f.mu.Unlock()
		return nil, artifactclient.ErrAlreadyExists
	}
	f.objects[key] = append([]byte(nil), value...)
	info := &artifactclient.ObjectInfo{
		Key: key, Size: int64(len(value)) + f.reportedSizeOffset, ContentType: contentType,
	}
	putErr := f.putErrAfterStore
	afterPut := f.afterPut
	f.mu.Unlock()
	if afterPut != nil {
		afterPut(key)
	}
	if putErr != nil {
		return nil, putErr
	}
	return info, nil
}

func (f *memoryCacheBlobs) Open(_ context.Context, key string) (*artifactclient.Object, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, exists := f.objects[key]
	if !exists {
		return nil, artifactclient.ErrNotFound
	}
	copyValue := append([]byte(nil), value...)
	return &artifactclient.Object{
		ObjectInfo: artifactclient.ObjectInfo{Key: key, Size: int64(len(copyValue)), ContentType: "application/octet-stream"},
		Body:       io.NopCloser(bytes.NewReader(copyValue)),
	}, nil
}

func (f *memoryCacheBlobs) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, exists := f.objects[key]; !exists {
		return artifactclient.ErrNotFound
	}
	delete(f.objects, key)
	return nil
}

func (f *memoryCacheBlobs) objectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects)
}

type cacheHandlerFixture struct {
	pool      *db.Pool
	pipelines *pipelinestore.Store
	runners   *runnerstore.Store
	runnerID  uuid.UUID
	token     string
	orgID     uuid.UUID
	projectID uuid.UUID
	repoID    uuid.UUID
	mux       http.Handler
	handler   *runnerhttp.Handler
	blobs     *memoryCacheBlobs
}

func newCacheHandlerFixture(t *testing.T, blobs *memoryCacheBlobs) *cacheHandlerFixture {
	t.Helper()
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ctx := context.Background()
	pipelines := pipelinestore.New(pool, t.TempDir())
	runners := runnerstore.New(pool)
	orgID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO orgs (id, slug) VALUES ($1, $2)`, orgID, "cache-org-"+orgID.String()[:8])
	require.NoError(t, err)
	projectID, repoID := seedCacheRepo(t, pool, orgID)
	runner, err := runners.CreateEphemeralRunner(ctx, orgID, "cache-runner", []string{"linux"}, model.TierMedium, "aws", "cache-pool", model.OSLinux)
	require.NoError(t, err)

	h := &runnerhttp.Handler{
		Runners: runners, Pipelines: pipelines, CacheBlobs: blobs,
		CacheMaxUploadBytes: 1024 * 1024, CacheTTL: time.Hour,
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) { h.Mount(api) })
	return &cacheHandlerFixture{
		pool: pool, pipelines: pipelines, runners: runners,
		runnerID: runner.ID, token: runner.Token, orgID: orgID,
		projectID: projectID, repoID: repoID, mux: r, handler: h, blobs: blobs,
	}
}

func seedCacheRepo(t *testing.T, pool *db.Pool, orgID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	projectID, repoID := uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, slug) VALUES ($1, $2, $3)`, projectID, orgID, "project-"+projectID.String()[:8])
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO repos (id, project_id, slug) VALUES ($1, $2, $3)`, repoID, projectID, "repo-"+repoID.String()[:8])
	require.NoError(t, err)
	return projectID, repoID
}

func (f *cacheHandlerFixture) createRunningJob(t *testing.T, projectID, repoID uuid.UUID, event string) uuid.UUID {
	t.Helper()
	wf, err := pipeline.Parse([]byte("name: cache\non: [push, pull_request]\njobs:\n  build:\n    steps: [{run: echo cache}]\n"))
	require.NoError(t, err)
	run, err := f.pipelines.CreateRun(context.Background(), pipelinestore.CreateRunParams{
		OrgID: f.orgID, ProjectID: projectID, RepoID: repoID,
		WorkflowPath: ".wuling/workflows/cache.yml", Event: event,
		GitRef: "refs/heads/main", CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		Workflow: wf, DefaultTier: model.TierMedium,
	})
	require.NoError(t, err)
	acquired, err := f.pipelines.AcquireJob(context.Background(), f.runnerID)
	require.NoError(t, err)
	require.NotNil(t, acquired)
	require.Equal(t, run.ID, acquired.RunID)
	return acquired.JobID
}

func (f *cacheHandlerFixture) request(method, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	return f.requestWithContentLength(method, path, body, contentType, int64(len(body)))
}

func (f *cacheHandlerFixture) requestWithContentLength(
	method, path string,
	body []byte,
	contentType string,
	contentLength int64,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.ContentLength = contentLength
	req.Header.Set("Authorization", "Bearer "+f.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *cacheHandlerFixture) cacheEntryCount(t *testing.T) int {
	t.Helper()
	var count int
	err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM pipeline_cache_entries`).Scan(&count)
	require.NoError(t, err)
	return count
}

func cacheSHA(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func TestRunnerCacheUploadRestoreAndRepoScope(t *testing.T) {
	blobs := newMemoryCacheBlobs()
	f := newCacheHandlerFixture(t, blobs)
	jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
	archive := []byte("cache archive")
	base := "/api/v1/runner/jobs/" + jobID.String() + "/cache"

	upload := f.request(http.MethodPut, base+"?key=linux-cargo&version=v1&sha256="+cacheSHA(archive), archive, "application/octet-stream")
	require.Equal(t, http.StatusCreated, upload.Code, upload.Body.String())

	existing := f.request(http.MethodPut, base+"?key=linux-cargo&version=v1&sha256="+cacheSHA(archive), archive, "application/octet-stream")
	require.Equal(t, http.StatusOK, existing.Code, existing.Body.String())

	restoreBody := []byte(`{"key":"linux-cargo","version":"v1","restore_keys":[]}`)
	restore := f.request(http.MethodPost, base+"/restore", restoreBody, "application/json")
	require.Equal(t, http.StatusOK, restore.Code, restore.Body.String())
	require.Equal(t, archive, restore.Body.Bytes())
	require.Equal(t, "linux-cargo", restore.Header().Get("X-Wuling-Cache-Key"))
	require.Equal(t, cacheSHA(archive), restore.Header().Get("X-Wuling-Cache-SHA256"))
	require.Equal(t, "13", restore.Header().Get("Content-Length"))

	otherProjectID, otherRepoID := seedCacheRepo(t, f.pool, f.orgID)
	otherJobID := f.createRunningJob(t, otherProjectID, otherRepoID, "push")
	otherBase := "/api/v1/runner/jobs/" + otherJobID.String() + "/cache"
	miss := f.request(http.MethodPost, otherBase+"/restore", restoreBody, "application/json")
	require.Equal(t, http.StatusNoContent, miss.Code, miss.Body.String())
}

func TestRunnerCacheUploadingReservationConflicts(t *testing.T) {
	blobs := newMemoryCacheBlobs()
	blobs.putStarted = make(chan struct{})
	blobs.putRelease = make(chan struct{})
	f := newCacheHandlerFixture(t, blobs)
	jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
	archive := []byte("concurrent cache")
	path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=concurrent&version=v1&sha256=" + cacheSHA(archive)

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- f.request(http.MethodPut, path, archive, "application/octet-stream")
	}()
	<-blobs.putStarted

	conflict := f.request(http.MethodPut, path, archive, "application/octet-stream")
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	close(blobs.putRelease)
	first := <-firstDone
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
}

func TestPullRequestCacheMayRestoreButNotUpload(t *testing.T) {
	blobs := newMemoryCacheBlobs()
	f := newCacheHandlerFixture(t, blobs)
	pushJobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
	archive := []byte("trusted cache")
	pushBase := "/api/v1/runner/jobs/" + pushJobID.String() + "/cache"
	upload := f.request(http.MethodPut, pushBase+"?key=trusted&version=v1&sha256="+cacheSHA(archive), archive, "application/octet-stream")
	require.Equal(t, http.StatusCreated, upload.Code, upload.Body.String())

	prJobID := f.createRunningJob(t, f.projectID, f.repoID, "pull_request")
	prBase := "/api/v1/runner/jobs/" + prJobID.String() + "/cache"
	forbidden := f.request(http.MethodPut, prBase+"?key=other&version=v1&sha256="+cacheSHA(archive), archive, "application/octet-stream")
	require.Equal(t, http.StatusForbidden, forbidden.Code, forbidden.Body.String())

	restore := f.request(http.MethodPost, prBase+"/restore", []byte(`{"key":"trusted","version":"v1","restore_keys":[]}`), "application/json")
	require.Equal(t, http.StatusOK, restore.Code, restore.Body.String())
	require.Equal(t, archive, restore.Body.Bytes())
}

func TestRunnerCacheUploadValidatesBodyAndCleansFailures(t *testing.T) {
	t.Run("chunked body over limit", func(t *testing.T) {
		blobs := newMemoryCacheBlobs()
		f := newCacheHandlerFixture(t, blobs)
		f.handler.CacheMaxUploadBytes = 4
		jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
		body := []byte("12345")
		path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=oversize&version=v1&sha256=" + cacheSHA(body)

		response := f.requestWithContentLength(http.MethodPut, path, body, "application/octet-stream", -1)
		require.Equal(t, http.StatusRequestEntityTooLarge, response.Code, response.Body.String())
		require.Zero(t, f.cacheEntryCount(t))
		require.Zero(t, blobs.objectCount())
	})

	t.Run("content length mismatch", func(t *testing.T) {
		blobs := newMemoryCacheBlobs()
		f := newCacheHandlerFixture(t, blobs)
		jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
		body := []byte("short")
		path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=short&version=v1&sha256=" + cacheSHA(body)

		response := f.requestWithContentLength(http.MethodPut, path, body, "application/octet-stream", int64(len(body)+1))
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		require.Zero(t, f.cacheEntryCount(t))
		require.Zero(t, blobs.objectCount())
	})

	t.Run("sha mismatch", func(t *testing.T) {
		blobs := newMemoryCacheBlobs()
		f := newCacheHandlerFixture(t, blobs)
		jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
		body := []byte("wrong digest")
		path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=digest&version=v1&sha256=" + strings.Repeat("0", 64)

		response := f.request(http.MethodPut, path, body, "application/octet-stream")
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		require.Zero(t, f.cacheEntryCount(t))
		require.Zero(t, blobs.objectCount())
	})

	t.Run("storage size mismatch", func(t *testing.T) {
		blobs := newMemoryCacheBlobs()
		blobs.reportedSizeOffset = 1
		f := newCacheHandlerFixture(t, blobs)
		jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
		body := []byte("bad size")
		path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=size&version=v1&sha256=" + cacheSHA(body)

		response := f.request(http.MethodPut, path, body, "application/octet-stream")
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		require.Zero(t, f.cacheEntryCount(t))
		require.Zero(t, blobs.objectCount())
	})

	t.Run("put failure after blob write", func(t *testing.T) {
		blobs := newMemoryCacheBlobs()
		blobs.putErrAfterStore = errors.New("response lost after write")
		f := newCacheHandlerFixture(t, blobs)
		jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
		body := []byte("put failure")
		path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=put-failure&version=v1&sha256=" + cacheSHA(body)

		response := f.request(http.MethodPut, path, body, "application/octet-stream")
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		require.Zero(t, f.cacheEntryCount(t))
		require.Zero(t, blobs.objectCount())
	})

	t.Run("mark ready failure", func(t *testing.T) {
		blobs := newMemoryCacheBlobs()
		f := newCacheHandlerFixture(t, blobs)
		blobs.afterPut = func(blobKey string) {
			_, err := f.pool.Exec(context.Background(), `
				UPDATE pipeline_cache_entries SET expires_at = now() - interval '1 second'
				WHERE blob_key = $1
			`, blobKey)
			require.NoError(t, err)
		}
		jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
		body := []byte("late upload")
		path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=late&version=v1&sha256=" + cacheSHA(body)

		response := f.request(http.MethodPut, path, body, "application/octet-stream")
		require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
		require.Zero(t, f.cacheEntryCount(t))
		require.Zero(t, blobs.objectCount())
	})

	t.Run("failed blob cleanup leaves expired metadata for GC", func(t *testing.T) {
		blobs := newMemoryCacheBlobs()
		blobs.putErrAfterStore = errors.New("response lost after write")
		blobs.deleteErr = errors.New("blob delete unavailable")
		f := newCacheHandlerFixture(t, blobs)
		jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
		body := []byte("retry cleanup")
		path := "/api/v1/runner/jobs/" + jobID.String() + "/cache?key=cleanup&version=v1&sha256=" + cacheSHA(body)

		response := f.request(http.MethodPut, path, body, "application/octet-stream")
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		require.Equal(t, 1, f.cacheEntryCount(t))
		require.Equal(t, 1, blobs.objectCount())
		var status string
		err := f.pool.QueryRow(context.Background(), `SELECT status FROM pipeline_cache_entries`).Scan(&status)
		require.NoError(t, err)
		require.Equal(t, pipelinestore.CacheStatusExpired, status)
	})
}

func TestRunnerCacheRestoreMissingBlobDeletesMetadata(t *testing.T) {
	blobs := newMemoryCacheBlobs()
	f := newCacheHandlerFixture(t, blobs)
	jobID := f.createRunningJob(t, f.projectID, f.repoID, "push")
	archive := []byte("soon missing")
	base := "/api/v1/runner/jobs/" + jobID.String() + "/cache"

	upload := f.request(http.MethodPut, base+"?key=missing-blob&version=v1&sha256="+cacheSHA(archive), archive, "application/octet-stream")
	require.Equal(t, http.StatusCreated, upload.Code, upload.Body.String())
	var blobKey string
	err := f.pool.QueryRow(context.Background(), `SELECT blob_key FROM pipeline_cache_entries`).Scan(&blobKey)
	require.NoError(t, err)
	require.NoError(t, blobs.Delete(context.Background(), blobKey))

	restore := f.request(http.MethodPost, base+"/restore", []byte(`{"key":"missing-blob","version":"v1"}`), "application/json")
	require.Equal(t, http.StatusNoContent, restore.Code, restore.Body.String())
	require.Zero(t, f.cacheEntryCount(t))
}

func TestMemoryCacheBlobsDeleteMissing(t *testing.T) {
	blobs := newMemoryCacheBlobs()
	require.True(t, errors.Is(blobs.Delete(context.Background(), "missing"), artifactclient.ErrNotFound))
}
