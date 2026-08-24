-- 0019_pipeline_cache: cross-runner pipeline cache metadata.
--
-- Cache bytes live in the existing Artifact Service. PostgreSQL owns the
-- repository namespace, upload reservation, immutable publication state, TTL,
-- and lookup order. An entry is visible only after it reaches ready.

CREATE TABLE pipeline_cache_entries (
    id                UUID PRIMARY KEY,
    org_id            UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repo_id           UUID NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    cache_key         TEXT NOT NULL
                      CHECK (OCTET_LENGTH(cache_key) BETWEEN 1 AND 512),
    cache_version     TEXT NOT NULL
                      CHECK (OCTET_LENGTH(cache_version) BETWEEN 1 AND 128),
    status            TEXT NOT NULL DEFAULT 'uploading'
                      CHECK (status IN ('uploading', 'ready', 'expired')),
    blob_key          TEXT NOT NULL CHECK (OCTET_LENGTH(blob_key) BETWEEN 1 AND 1024),
    size_bytes        BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    sha256            TEXT NOT NULL DEFAULT '',
    created_by_job_id UUID REFERENCES pipeline_jobs(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_accessed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL,
    CONSTRAINT pipeline_cache_blob_key_uq UNIQUE (blob_key),
    CONSTRAINT pipeline_cache_state_metadata_chk CHECK (
        (status = 'uploading' AND size_bytes = 0 AND sha256 = '') OR
        (status = 'ready' AND sha256 ~ '^[0-9a-f]{64}$') OR
        (status = 'expired' AND (
            (size_bytes = 0 AND sha256 = '') OR sha256 ~ '^[0-9a-f]{64}$'
        ))
    )
);

-- At most one active publisher/value exists for a repository key and archive
-- version. Expired rows stay addressable by GC without blocking a new writer.
CREATE UNIQUE INDEX pipeline_cache_active_key_uq
    ON pipeline_cache_entries (repo_id, cache_key, cache_version)
    WHERE status IN ('uploading', 'ready');

CREATE INDEX pipeline_cache_restore_idx
    ON pipeline_cache_entries (repo_id, cache_version, created_at DESC, id DESC)
    WHERE status = 'ready';

CREATE INDEX pipeline_cache_expiry_idx
    ON pipeline_cache_entries (status, expires_at, id);
