package runnerhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/artifactclient"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
)

const (
	MaxPipelineCacheKeyBytes     = 512
	MaxPipelineCacheVersionBytes = 128
	MaxPipelineCacheRestoreKeys  = 10
	DefaultPipelineCacheMaxBytes = int64(512 * 1024 * 1024)
	DefaultPipelineCacheTTL      = 14 * 24 * time.Hour
	pipelineCacheReservationTTL  = time.Hour
	pipelineCacheCleanupTimeout  = 30 * time.Second
)

// CacheBlobStore is the byte-storage boundary used by runner cache handlers.
// artifactclient.Client implements it; tests use an in-memory fake.
type CacheBlobStore interface {
	Put(context.Context, string, io.Reader, int64, string) (*artifactclient.ObjectInfo, error)
	Open(context.Context, string) (*artifactclient.Object, error)
	Delete(context.Context, string) error
}

type restoreCacheReq struct {
	Key         string   `json:"key"`
	Version     string   `json:"version"`
	RestoreKeys []string `json:"restore_keys"`
}

func (h *Handler) restoreCache(w http.ResponseWriter, r *http.Request) {
	jc, err := h.ownedJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if h.CacheBlobs == nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "pipeline cache storage is unavailable"))
		return
	}
	var req restoreCacheReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := validateCacheLookup(req.Key, req.Version, req.RestoreKeys); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	entry, err := h.Pipelines.FindCache(r.Context(), jc.RepoID, req.Key, req.Version, req.RestoreKeys)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if entry == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	object, err := h.CacheBlobs.Open(r.Context(), entry.BlobKey)
	if err != nil {
		if errors.Is(err, artifactclient.ErrNotFound) {
			// A ready row without bytes cannot be restored. Drop the broken
			// metadata so the same logical key can be published again.
			if err := h.Pipelines.DeleteCacheEntry(r.Context(), entry.ID); err != nil {
				httpapi.RenderError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeUnavailable, "pipeline cache download failed", err))
		return
	}
	defer object.Body.Close()
	if object.Size >= 0 && object.Size != entry.SizeBytes {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "pipeline cache object size does not match metadata"))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Wuling-Cache-Key", entry.Key)
	w.Header().Set("X-Wuling-Cache-SHA256", entry.SHA256)
	w.Header().Set("Content-Length", strconv.FormatInt(entry.SizeBytes, 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, object.Body); err != nil {
		slog.Warn("pipeline cache response stream failed", "entry_id", entry.ID, "err", err)
		return
	}
	if err := h.Pipelines.TouchCache(r.Context(), entry.ID); err != nil {
		slog.Warn("pipeline cache touch failed", "entry_id", entry.ID, "err", err)
	}
}

func (h *Handler) uploadCache(w http.ResponseWriter, r *http.Request) {
	jc, err := h.ownedJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if jc.Event == "pull_request" {
		httpapi.RenderError(w, r, apperr.Forbidden("pull request jobs may restore but not publish pipeline caches"))
		return
	}
	if h.CacheBlobs == nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "pipeline cache storage is unavailable"))
		return
	}

	key := r.URL.Query().Get("key")
	version := r.URL.Query().Get("version")
	expectedSHA, err := validateCacheUpload(key, version, r.URL.Query().Get("sha256"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	maxBytes := h.cacheMaxUploadBytes()
	if r.ContentLength > maxBytes {
		httpapi.RenderError(w, r, apperr.New(apperr.CodePayloadTooLarge, "pipeline cache exceeds upload limit"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	reservationTTL := pipelineCacheReservationTTL
	if ttl := h.cacheTTL(); ttl < reservationTTL {
		reservationTTL = ttl
	}
	reservation, err := h.Pipelines.ReserveCache(r.Context(), pipelinestore.ReserveCacheParams{
		OrgID: jc.OrgID, ProjectID: jc.ProjectID, RepoID: jc.RepoID, JobID: jc.JobID,
		Key: key, Version: version, ReservationTTL: reservationTTL,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	switch reservation.State {
	case pipelinestore.CacheReservationReady:
		w.WriteHeader(http.StatusOK)
		return
	case pipelinestore.CacheReservationUploading:
		httpapi.RenderError(w, r, apperr.Conflict("pipeline cache upload is already in progress"))
		return
	}

	hasher := sha256.New()
	counted := &countingReader{reader: io.TeeReader(r.Body, hasher)}
	declaredSize := r.ContentLength
	if declaredSize < 0 {
		declaredSize = -1
	}
	info, putErr := h.CacheBlobs.Put(
		r.Context(), reservation.Entry.BlobKey, counted, declaredSize, "application/octet-stream",
	)
	if putErr != nil {
		h.abandonCacheUpload(reservation.Entry)
		httpapi.RenderError(w, r, classifyCacheUploadError(putErr, counted.readErr))
		return
	}

	// A successful blob store must consume the complete request body. Draining
	// the bounded reader catches short-consuming test doubles and malformed
	// direct requests before immutable metadata becomes visible.
	if _, err := io.Copy(io.Discard, counted); err != nil {
		h.abandonCacheUpload(reservation.Entry)
		httpapi.RenderError(w, r, classifyCacheUploadError(nil, err))
		return
	}
	if declaredSize >= 0 && counted.n != declaredSize {
		h.abandonCacheUpload(reservation.Entry)
		httpapi.RenderError(w, r, apperr.Validation("pipeline cache body length does not match Content-Length", nil))
		return
	}
	if info == nil || info.Size != counted.n {
		h.abandonCacheUpload(reservation.Entry)
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "pipeline cache storage returned an inconsistent size"))
		return
	}
	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if actualSHA != expectedSHA {
		h.abandonCacheUpload(reservation.Entry)
		httpapi.RenderError(w, r, apperr.Validation("pipeline cache sha256 does not match request body", nil))
		return
	}
	if err := h.Pipelines.MarkCacheReady(r.Context(), reservation.Entry.ID, counted.n, actualSHA, h.cacheTTL()); err != nil {
		h.abandonCacheUpload(reservation.Entry)
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) abandonCacheUpload(entry pipelinestore.PipelineCacheEntry) {
	abandonCtx, abandonCancel := context.WithTimeout(context.Background(), pipelineCacheCleanupTimeout)
	if err := h.Pipelines.AbandonCache(abandonCtx, entry.ID); err != nil {
		slog.Warn("abandon pipeline cache reservation failed", "entry_id", entry.ID, "err", err)
	}
	abandonCancel()

	if h.CacheBlobs == nil {
		return
	}
	blobCtx, blobCancel := context.WithTimeout(context.Background(), pipelineCacheCleanupTimeout)
	err := h.CacheBlobs.Delete(blobCtx, entry.BlobKey)
	blobCancel()
	if err != nil && !errors.Is(err, artifactclient.ErrNotFound) {
		slog.Warn("delete abandoned pipeline cache blob failed", "entry_id", entry.ID, "blob_key", entry.BlobKey, "err", err)
		return
	}

	metadataCtx, metadataCancel := context.WithTimeout(context.Background(), pipelineCacheCleanupTimeout)
	defer metadataCancel()
	if err := h.Pipelines.DeleteCacheEntry(metadataCtx, entry.ID); err != nil {
		slog.Warn("delete abandoned pipeline cache metadata failed", "entry_id", entry.ID, "err", err)
	}
}

func (h *Handler) cacheMaxUploadBytes() int64 {
	if h.CacheMaxUploadBytes > 0 {
		return h.CacheMaxUploadBytes
	}
	return DefaultPipelineCacheMaxBytes
}

func (h *Handler) cacheTTL() time.Duration {
	if h.CacheTTL > 0 {
		return h.CacheTTL
	}
	return DefaultPipelineCacheTTL
}

func validateCacheLookup(key, version string, restoreKeys []string) error {
	if err := validateCacheText("key", key, MaxPipelineCacheKeyBytes); err != nil {
		return err
	}
	if err := validateCacheText("version", version, MaxPipelineCacheVersionBytes); err != nil {
		return err
	}
	if len(restoreKeys) > MaxPipelineCacheRestoreKeys {
		return apperr.Validation("restore_keys exceeds the maximum of 10 entries", nil)
	}
	for _, prefix := range restoreKeys {
		if err := validateCacheText("restore_keys", prefix, MaxPipelineCacheKeyBytes); err != nil {
			return err
		}
	}
	return nil
}

func validateCacheUpload(key, version, rawSHA string) (string, error) {
	if err := validateCacheLookup(key, version, nil); err != nil {
		return "", err
	}
	sha := strings.ToLower(rawSHA)
	decoded, err := hex.DecodeString(sha)
	if err != nil || len(decoded) != sha256.Size {
		return "", apperr.Validation("sha256 must be 64 hexadecimal characters", nil)
	}
	return sha, nil
}

func classifyCacheUploadError(putErr, bodyErr error) error {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(putErr, artifactclient.ErrTooLarge), errors.As(putErr, &maxErr), errors.As(bodyErr, &maxErr):
		return apperr.New(apperr.CodePayloadTooLarge, "pipeline cache exceeds upload limit")
	case bodyErr != nil:
		return apperr.Wrap(apperr.CodeBadRequest, "failed to read pipeline cache body", bodyErr)
	case errors.Is(putErr, artifactclient.ErrAlreadyExists):
		return apperr.Conflict("pipeline cache blob already exists")
	default:
		return apperr.Wrap(apperr.CodeUnavailable, "pipeline cache upload failed", putErr)
	}
}

func validateCacheText(name, value string, maxBytes int) error {
	if value == "" {
		return apperr.Validation(name+" is required", nil)
	}
	if len(value) > maxBytes {
		return apperr.Validation(name+" exceeds the maximum length", nil)
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return apperr.Validation(name+" must contain printable ASCII characters only", nil)
		}
	}
	return nil
}

type countingReader struct {
	reader  io.Reader
	n       int64
	readErr error
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.n += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}
	return n, err
}
