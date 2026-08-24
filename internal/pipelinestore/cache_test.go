//go:build cgo

package pipelinestore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
)

const cacheTestWorkflow = `
name: cache-test
on: push
jobs:
  build:
    steps: [{run: echo test}]
`

func TestPipelineCacheReservationFirstWriterWins(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, store, orgID, projectID, repoID, cacheTestWorkflow)
	require.Len(t, run.Jobs, 1)
	jobID := run.Jobs[0].ID
	ctx := context.Background()

	params := pipelinestore.ReserveCacheParams{
		OrgID: orgID, ProjectID: projectID, RepoID: repoID, JobID: jobID,
		Key: "linux-cargo-lock", Version: "archive-v1", ReservationTTL: time.Hour,
	}
	first, err := store.ReserveCache(ctx, params)
	require.NoError(t, err)
	require.Equal(t, pipelinestore.CacheReservationCreated, first.State)
	require.Equal(t, pipelinestore.CacheStatusUploading, first.Entry.Status)

	concurrent, err := store.ReserveCache(ctx, params)
	require.NoError(t, err)
	require.Equal(t, pipelinestore.CacheReservationUploading, concurrent.State)
	require.Equal(t, first.Entry.ID, concurrent.Entry.ID)

	digest := strings.Repeat("a", 64)
	require.NoError(t, store.MarkCacheReady(ctx, first.Entry.ID, 123, digest, 14*24*time.Hour))

	existing, err := store.ReserveCache(ctx, params)
	require.NoError(t, err)
	require.Equal(t, pipelinestore.CacheReservationReady, existing.State)
	require.Equal(t, first.Entry.ID, existing.Entry.ID)

	found, err := store.FindCache(ctx, repoID, params.Key, params.Version, nil)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, digest, found.SHA256)
	require.Equal(t, int64(123), found.SizeBytes)
}

func TestPipelineCacheRestoreOrderAndRepoIsolation(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, store, orgID, projectID, repoID, cacheTestWorkflow)
	jobID := run.Jobs[0].ID
	ctx := context.Background()

	publish := func(key string, createdAt time.Time) pipelinestore.PipelineCacheEntry {
		t.Helper()
		reservation, err := store.ReserveCache(ctx, pipelinestore.ReserveCacheParams{
			OrgID: orgID, ProjectID: projectID, RepoID: repoID, JobID: jobID,
			Key: key, Version: "archive-v1", ReservationTTL: time.Hour,
		})
		require.NoError(t, err)
		require.Equal(t, pipelinestore.CacheReservationCreated, reservation.State)
		require.NoError(t, store.MarkCacheReady(ctx, reservation.Entry.ID, 1, strings.Repeat("b", 64), time.Hour))
		_, err = pool.Exec(ctx, `UPDATE pipeline_cache_entries SET created_at = $2 WHERE id = $1`, reservation.Entry.ID, createdAt)
		require.NoError(t, err)
		return reservation.Entry
	}

	base := time.Now().Add(-time.Hour)
	publish("linux-cargo-old", base)
	newestLinux := publish("linux-cargo-new", base.Add(2*time.Minute))
	other := publish("fallback-other", base.Add(time.Minute))

	found, err := store.FindCache(ctx, repoID, "missing", "archive-v1", []string{"fallback-", "linux-cargo-"})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, other.ID, found.ID, "restore-key declaration order must win before recency")

	found, err = store.FindCache(ctx, repoID, "missing", "archive-v1", []string{"linux-cargo-"})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, newestLinux.ID, found.ID)

	literalPercent := publish("literal%match", base.Add(3*time.Minute))
	publish("literalXnewer", base.Add(4*time.Minute))
	found, err = store.FindCache(ctx, repoID, "missing", "archive-v1", []string{"literal%"})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, literalPercent.ID, found.ID, "restore prefixes must treat LIKE wildcards literally")

	literalUnderscore := publish("under_score", base.Add(5*time.Minute))
	publish("underXnewer", base.Add(6*time.Minute))
	found, err = store.FindCache(ctx, repoID, "missing", "archive-v1", []string{"under_"})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, literalUnderscore.ID, found.ID, "restore prefixes must treat LIKE wildcards literally")

	_, _, otherRepoID := seedRepo(t, pool)
	found, err = store.FindCache(ctx, otherRepoID, "linux-cargo-new", "archive-v1", []string{"linux-"})
	require.NoError(t, err)
	require.Nil(t, found)

	_, err = pool.Exec(ctx, `UPDATE pipeline_cache_entries SET expires_at = now() - interval '1 second' WHERE id = $1`, newestLinux.ID)
	require.NoError(t, err)
	found, err = store.FindCache(ctx, repoID, "linux-cargo-new", "archive-v1", nil)
	require.NoError(t, err)
	require.Nil(t, found)
}

func TestPipelineCacheGCListsAndDeletesExpiredMetadata(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := pipelinestore.New(pool, t.TempDir())
	orgID, projectID, repoID := seedRepo(t, pool)
	run := createRun(t, store, orgID, projectID, repoID, cacheTestWorkflow)
	ctx := context.Background()

	reservation, err := store.ReserveCache(ctx, pipelinestore.ReserveCacheParams{
		OrgID: orgID, ProjectID: projectID, RepoID: repoID, JobID: run.Jobs[0].ID,
		Key: "expired", Version: "archive-v1", ReservationTTL: time.Hour,
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE pipeline_cache_entries SET expires_at = now() - interval '1 second' WHERE id = $1`, reservation.Entry.ID)
	require.NoError(t, err)

	expired, err := store.ListExpiredCaches(ctx, 100)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	require.Equal(t, reservation.Entry.ID, expired[0].ID)
	require.Equal(t, pipelinestore.CacheStatusExpired, expired[0].Status)

	require.NoError(t, store.DeleteCacheEntry(ctx, reservation.Entry.ID))
	expired, err = store.ListExpiredCaches(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, expired)
}
