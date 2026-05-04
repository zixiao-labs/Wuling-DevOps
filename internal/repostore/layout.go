// Package repostore wires the on-disk repo layout to the database. Repos live
// at "<root>/<orgID>/<projectID>/<repoID>.git". IDs (not slugs) keep renames
// cheap.
package repostore

import (
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
