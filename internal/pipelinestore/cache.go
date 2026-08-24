package pipelinestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

const (
	CacheStatusUploading = "uploading"
	CacheStatusReady     = "ready"
	CacheStatusExpired   = "expired"

	CacheReservationCreated   = "created"
	CacheReservationReady     = "ready"
	CacheReservationUploading = "uploading"
)

// PipelineCacheEntry is one repository-scoped cache object. Bytes are stored
// by the Artifact Service under BlobKey; this row controls visibility and TTL.
type PipelineCacheEntry struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	ProjectID      uuid.UUID
	RepoID         uuid.UUID
	Key            string
	Version        string
	Status         string
	BlobKey        string
	SizeBytes      int64
	SHA256         string
	CreatedByJobID *uuid.UUID
	CreatedAt      time.Time
	LastAccessedAt time.Time
	ExpiresAt      time.Time
}

// ReserveCacheParams identifies the authoritative job/repository scope and the
// logical key a runner wants to publish.
type ReserveCacheParams struct {
	OrgID          uuid.UUID
	ProjectID      uuid.UUID
	RepoID         uuid.UUID
	JobID          uuid.UUID
	Key            string
	Version        string
	ReservationTTL time.Duration
}

// CacheReservation reports whether the caller created an upload reservation or
// raced an already ready/uploading entry.
type CacheReservation struct {
	State string
	Entry PipelineCacheEntry
}

// ReserveCache serializes publishers for one repository key/version. Expired
// active rows are retired first so they remain discoverable by GC without
// blocking a replacement. A ready or uploading entry is never overwritten.
func (s *Store) ReserveCache(ctx context.Context, p ReserveCacheParams) (*CacheReservation, error) {
	if p.ReservationTTL <= 0 {
		return nil, apperr.Validation("cache reservation TTL must be positive", nil)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// PostgreSQL text values cannot contain NUL. Length-prefix the variable
	// components so different key/version pairs cannot form the same input
	// before hashing (an advisory-lock hash collision would only over-serialize).
	lockKey := fmt.Sprintf("%s:%d:%s:%d:%s", p.RepoID, len(p.Version), p.Version, len(p.Key), p.Key)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return nil, apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pipeline_cache_entries
		SET status = 'expired'
		WHERE repo_id = $1 AND cache_key = $2 AND cache_version = $3
		  AND status IN ('uploading', 'ready') AND expires_at <= now()
	`, p.RepoID, p.Key, p.Version); err != nil {
		return nil, apperr.Internal(err)
	}

	entry, err := scanCacheEntry(tx.QueryRow(ctx, `
		SELECT id, org_id, project_id, repo_id, cache_key, cache_version, status,
		       blob_key, size_bytes, sha256, created_by_job_id, created_at,
		       last_accessed_at, expires_at
		FROM pipeline_cache_entries
		WHERE repo_id = $1 AND cache_key = $2 AND cache_version = $3
		  AND status IN ('uploading', 'ready')
		FOR UPDATE
	`, p.RepoID, p.Key, p.Version))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, apperr.Internal(err)
		}
		state := CacheReservationUploading
		if entry.Status == CacheStatusReady {
			state = CacheReservationReady
		}
		return &CacheReservation{State: state, Entry: *entry}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Internal(err)
	}

	id := uuid.New()
	blobKey := cacheBlobKey(p.OrgID, p.ProjectID, p.RepoID, id)
	reservationExpiresAt := time.Now().Add(p.ReservationTTL)
	entry, err = scanCacheEntry(tx.QueryRow(ctx, `
		INSERT INTO pipeline_cache_entries
		    (id, org_id, project_id, repo_id, cache_key, cache_version, status,
		     blob_key, created_by_job_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,'uploading',$7,$8,$9)
		RETURNING id, org_id, project_id, repo_id, cache_key, cache_version, status,
		          blob_key, size_bytes, sha256, created_by_job_id, created_at,
		          last_accessed_at, expires_at
	`, id, p.OrgID, p.ProjectID, p.RepoID, p.Key, p.Version, blobKey, p.JobID, reservationExpiresAt))
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	return &CacheReservation{State: CacheReservationCreated, Entry: *entry}, nil
}

// MarkCacheReady atomically publishes a completed immutable blob. Only the
// reservation that is still uploading can transition to ready.
func (s *Store) MarkCacheReady(ctx context.Context, id uuid.UUID, size int64, sha256 string, ttl time.Duration) error {
	if ttl <= 0 {
		return apperr.Validation("cache TTL must be positive", nil)
	}
	expiresAt := time.Now().Add(ttl)
	tag, err := s.pool.Exec(ctx, `
		UPDATE pipeline_cache_entries
		SET status = 'ready', size_bytes = $2, sha256 = $3,
		    last_accessed_at = now(), expires_at = $4
		WHERE id = $1 AND status = 'uploading' AND expires_at > now()
	`, id, size, sha256, expiresAt)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.Conflict("cache upload reservation is no longer active")
	}
	return nil
}

// AbandonCache retires an upload reservation while preserving its blob key for
// GC. This frees the logical key immediately even if blob deletion is retried.
func (s *Store) AbandonCache(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_cache_entries
		SET status = 'expired', expires_at = now()
		WHERE id = $1 AND status = 'uploading'
	`, id)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// FindCache applies exact-key lookup first, followed by restore-key prefixes in
// caller order. Prefix matches choose the newest ready, unexpired entry.
func (s *Store) FindCache(ctx context.Context, repoID uuid.UUID, key, version string, restoreKeys []string) (*PipelineCacheEntry, error) {
	entry, err := s.findExactCache(ctx, repoID, key, version)
	if err == nil {
		return entry, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Internal(err)
	}
	for _, prefix := range restoreKeys {
		entry, err = scanCacheEntry(s.pool.QueryRow(ctx, `
			SELECT id, org_id, project_id, repo_id, cache_key, cache_version, status,
			       blob_key, size_bytes, sha256, created_by_job_id, created_at,
			       last_accessed_at, expires_at
			FROM pipeline_cache_entries
			WHERE repo_id = $1 AND cache_version = $2 AND status = 'ready'
			  AND expires_at > now()
			  AND LEFT(cache_key, CHAR_LENGTH($3)) = $3
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		`, repoID, version, prefix))
		if err == nil {
			return entry, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Internal(err)
		}
	}
	return nil, nil
}

func (s *Store) findExactCache(ctx context.Context, repoID uuid.UUID, key, version string) (*PipelineCacheEntry, error) {
	return scanCacheEntry(s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, repo_id, cache_key, cache_version, status,
		       blob_key, size_bytes, sha256, created_by_job_id, created_at,
		       last_accessed_at, expires_at
		FROM pipeline_cache_entries
		WHERE repo_id = $1 AND cache_key = $2 AND cache_version = $3
		  AND status = 'ready' AND expires_at > now()
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, repoID, key, version))
}

// TouchCache records a successful restore. It is deliberately best-effort at
// call sites because cache accounting must not break a job restore.
func (s *Store) TouchCache(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE pipeline_cache_entries SET last_accessed_at = now()
		WHERE id = $1 AND status = 'ready'
	`, id)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// ListExpiredCaches retires newly expired reservations/entries and returns a
// bounded batch whose blobs are ready for deletion by the GC worker.
func (s *Store) ListExpiredCaches(ctx context.Context, limit int) ([]PipelineCacheEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE pipeline_cache_entries
		SET status = 'expired'
		WHERE status IN ('uploading', 'ready') AND expires_at <= now()
	`); err != nil {
		return nil, apperr.Internal(err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, repo_id, cache_key, cache_version, status,
		       blob_key, size_bytes, sha256, created_by_job_id, created_at,
		       last_accessed_at, expires_at
		FROM pipeline_cache_entries
		WHERE status = 'expired'
		ORDER BY expires_at ASC, id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	entries := make([]PipelineCacheEntry, 0)
	for rows.Next() {
		entry, err := scanCacheEntry(rows)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		entries = append(entries, *entry)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return entries, nil
}

// DeleteCacheEntry removes metadata after its blob is gone (or confirmed
// missing). It is intentionally idempotent for concurrent GC workers.
func (s *Store) DeleteCacheEntry(ctx context.Context, id uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM pipeline_cache_entries WHERE id = $1`, id); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

type cacheRow interface {
	Scan(dest ...any) error
}

func scanCacheEntry(row cacheRow) (*PipelineCacheEntry, error) {
	var entry PipelineCacheEntry
	err := row.Scan(
		&entry.ID, &entry.OrgID, &entry.ProjectID, &entry.RepoID,
		&entry.Key, &entry.Version, &entry.Status, &entry.BlobKey,
		&entry.SizeBytes, &entry.SHA256, &entry.CreatedByJobID,
		&entry.CreatedAt, &entry.LastAccessedAt, &entry.ExpiresAt,
	)
	return &entry, err
}

func cacheBlobKey(orgID, projectID, repoID, entryID uuid.UUID) string {
	return fmt.Sprintf("pipeline-cache/%s/%s/%s/%s.cache", orgID, projectID, repoID, entryID)
}
