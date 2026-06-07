package pipelinestore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// MaxLogReadBytes caps a single ReadLog response so a huge log can't be slurped
// into one request; clients page with the returned offset.
const MaxLogReadBytes = 256 * 1024

// logPath shards by the first byte of the job id to avoid one fat directory.
func (s *Store) logPath(jobID uuid.UUID) string {
	id := jobID.String()
	return filepath.Join(s.logDir, id[:2], id+".log")
}

// AppendLog appends a log chunk for a job and updates log_size. Appends for one
// job arrive serially from its single runner, so no per-file lock is needed.
func (s *Store) AppendLog(ctx context.Context, jobID uuid.UUID, data []byte) (int64, error) {
	p := s.logPath(jobID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, apperr.Internal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return 0, apperr.Internal(err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return 0, apperr.Internal(err)
	}
	size := info.Size()
	// Close before recording the size: a deferred write error surfaces here and
	// must fail the append rather than persist a log_size we didn't fully write.
	if err := f.Close(); err != nil {
		return 0, apperr.Internal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE pipeline_jobs SET log_size = $2 WHERE id = $1`, jobID, size); err != nil {
		return 0, apperr.Internal(err)
	}
	return size, nil
}

// ReadLog returns up to MaxLogReadBytes of a job's log starting at offset,
// plus the next offset to read from. A missing file (job never logged) reads
// as empty. Used by both the range endpoint and the SSE tail.
func (s *Store) ReadLog(ctx context.Context, jobID uuid.UUID, offset int64, limit int) ([]byte, int64, error) {
	if limit <= 0 || limit > MaxLogReadBytes {
		limit = MaxLogReadBytes
	}
	if offset < 0 {
		offset = 0
	}
	f, err := os.Open(s.logPath(jobID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, offset, nil
		}
		return nil, offset, apperr.Internal(err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, apperr.Internal(err)
	}
	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, offset, apperr.Internal(err)
	}
	return buf[:n], offset + int64(n), nil
}
