// Package repostore wires the on-disk repo layout to the database. Repos live
// at "<root>/<orgID>/<projectID>/<repoID>.git". IDs (not slugs) keep renames
// cheap.
package repostore

import (
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
// Errors during the walk other than the root being absent are surfaced; an
// absent root path returns size 0 with no error so the caller can fall back
// to the existing DB value rather than blowing up. Symlinks aren't followed.
func DirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// If the root itself doesn't exist, propagate so callers can
			// distinguish "couldn't even open" from "empty repo".
			if p == path {
				return err
			}
			// Otherwise skip the offending entry.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
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
