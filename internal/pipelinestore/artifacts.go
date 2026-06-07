package pipelinestore

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// MaxArtifactBytes caps a single artifact upload. Stage 1 stores artifacts on
// local disk (sibling of the log dir); a later stage moves them to object
// storage.
const MaxArtifactBytes = 512 * 1024 * 1024 // 512 MiB

// artifactDir is a sibling of logDir so the two storage roots stay adjacent
// and easy to mount/back up together.
func (s *Store) artifactDir() string {
	return filepath.Join(filepath.Dir(s.logDir), "pipeline-artifacts")
}

func (s *Store) artifactPath(jobID uuid.UUID, name string) string {
	return filepath.Join(s.artifactDir(), jobID.String(), sanitizeArtifactName(name))
}

// SaveArtifact streams an uploaded artifact to disk, capping at
// MaxArtifactBytes. The name is sanitized to a single path component so a
// malicious runner can't traverse out of the artifact dir.
func (s *Store) SaveArtifact(jobID uuid.UUID, name string, r io.Reader) (int64, error) {
	p := s.artifactPath(jobID, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, apperr.Internal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxArtifactBytes+1))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(p)
		return 0, apperr.Internal(err)
	}
	if n > MaxArtifactBytes {
		_ = f.Close()
		_ = os.Remove(p)
		return 0, apperr.Validation("artifact exceeds size limit", nil)
	}
	// Close can surface a deferred write error (flush/fsync); treat it like any
	// other write failure rather than leaving a silently-truncated artifact.
	if err := f.Close(); err != nil {
		_ = os.Remove(p)
		return 0, apperr.Internal(err)
	}
	return n, nil
}

// OpenArtifact opens a stored artifact for download. Caller closes the file.
func (s *Store) OpenArtifact(jobID uuid.UUID, name string) (*os.File, error) {
	f, err := os.Open(s.artifactPath(jobID, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, apperr.NotFound("artifact")
		}
		return nil, apperr.Internal(err)
	}
	return f, nil
}

// sanitizeArtifactName reduces name to a safe single filename. Empty or
// traversal-only names fall back to "artifact".
func sanitizeArtifactName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "artifact"
	}
	return name
}
