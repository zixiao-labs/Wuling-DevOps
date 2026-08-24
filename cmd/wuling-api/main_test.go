package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/artifactclient"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
)

type fakePipelineCacheGCStore struct {
	entries      []pipelinestore.PipelineCacheEntry
	listErr      error
	deleteErr    map[uuid.UUID]error
	deleted      []uuid.UUID
	listCalls    int
	cancelOnList context.CancelFunc
}

func (f *fakePipelineCacheGCStore) ListExpiredCaches(_ context.Context, limit int) ([]pipelinestore.PipelineCacheEntry, error) {
	f.listCalls++
	if f.cancelOnList != nil {
		f.cancelOnList()
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.entries) > limit {
		return append([]pipelinestore.PipelineCacheEntry(nil), f.entries[:limit]...), nil
	}
	return append([]pipelinestore.PipelineCacheEntry(nil), f.entries...), nil
}

func (f *fakePipelineCacheGCStore) DeleteCacheEntry(_ context.Context, id uuid.UUID) error {
	if err := f.deleteErr[id]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

type fakePipelineCacheGCBlobs struct {
	deleteErr map[string]error
	deleted   []string
}

func (f *fakePipelineCacheGCBlobs) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return f.deleteErr[key]
}

func cacheGCTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCollectPipelineCacheBatchDeletesMetadataOnlyAfterBlob(t *testing.T) {
	missingID := uuid.New()
	failedID := uuid.New()
	metadataFailedID := uuid.New()
	store := &fakePipelineCacheGCStore{
		entries: []pipelinestore.PipelineCacheEntry{
			{ID: missingID, BlobKey: "missing"},
			{ID: failedID, BlobKey: "delete-fails"},
			{ID: metadataFailedID, BlobKey: "metadata-delete-fails"},
		},
		deleteErr: map[uuid.UUID]error{metadataFailedID: errors.New("database unavailable")},
	}
	blobs := &fakePipelineCacheGCBlobs{deleteErr: map[string]error{
		"missing":      artifactclient.ErrNotFound,
		"delete-fails": errors.New("blob storage unavailable"),
	}}

	collectPipelineCacheBatch(context.Background(), store, blobs, cacheGCTestLogger())

	require.Equal(t, []string{"missing", "delete-fails", "metadata-delete-fails"}, blobs.deleted)
	require.Equal(t, []uuid.UUID{missingID}, store.deleted)
}

func TestRunPipelineCacheGCCollectsImmediatelyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakePipelineCacheGCStore{cancelOnList: cancel}
	blobs := &fakePipelineCacheGCBlobs{}

	runPipelineCacheGC(ctx, store, blobs, time.Hour, cacheGCTestLogger())

	require.Equal(t, 1, store.listCalls)
}

func TestRunPipelineCacheGCRejectsInvalidInterval(t *testing.T) {
	store := &fakePipelineCacheGCStore{}
	blobs := &fakePipelineCacheGCBlobs{}

	runPipelineCacheGC(context.Background(), store, blobs, 0, cacheGCTestLogger())

	require.Zero(t, store.listCalls)
}
