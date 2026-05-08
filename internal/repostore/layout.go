// Package repostore wires the on-disk repo layout to the database. Repos live
// at "<root>/<orgID>/<projectID>/<repoID>.git". IDs (not slugs) keep renames
// cheap.
package repostore

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/google/uuid"
)

// Layout knows where to put repos on disk.
type Layout struct {
	Root string
}

// New returns a Layout rooted at root.
func New(root string) *Layout { return &Layout{Root: root} }

// Path returns the absolute filesystem path for the given identifiers.
func (l *Layout) Path(orgID, projectID, repoID uuid.UUID) string {
	return filepath.Join(l.Root, orgID.String(), projectID.String(), repoID.String()+".git")
}

// DirSize returns the total byte size of all regular files under path,
// recursively. Used after a merge to keep repos.size_bytes roughly in sync —
// "roughly" because Git's pack files compress, so the on-disk size lags real
// content size, but it's enough for UI sorting and quota signals.
//
// An absent root path returns size 0 with no error so the caller can fall
// back to the existing DB value rather than blowing up. Any other traversal
// error is surfaced. Symlinks aren't followed.
func DirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Absent root → 0,nil. Other root errors (permission denied,
			// I/O) propagate so the caller sees the real failure.
			if p == path && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			// Best-effort: a concurrent delete (e.g. git GC removing a pack
			// file mid-walk) shouldn't fail the whole estimate. Intentionally
			// ignored — silences golangci-lint nilerr; do not "fix".
			return nil
		}
		// Mode().IsRegular() filters symlinks, sockets, devices, etc.
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
