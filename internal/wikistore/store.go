// Package wikistore manages the per-project Markdown wiki, which is backed
// by its own bare Git repository on disk (one per project). The repo is
// created lazily on the first write, so projects that never touch their
// wiki cost zero bytes.
//
// All edits flow through libgit2 (internal/git.CommitFile / DeleteFile) so
// the wiki is clone-able via the regular Git smart-HTTP / SSH transports —
// "wiki repo on disk" and "wiki API" are the same bare repo viewed two ways.
package wikistore

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
)

// DefaultBranch is the branch every wiki commits onto. We don't surface this
// as a knob — the wiki has one branch and that branch is "main".
const DefaultBranch = "main"

// defaultRef is the fully-qualified ref name passed to libgit2.
const defaultRef = "refs/heads/" + DefaultBranch

// MaxPageBytes caps a single page's raw size. Wikis aren't binary blob
// stores; an oversized payload usually means a client is trying to use the
// wiki as one.
const MaxPageBytes = 1 << 20 // 1 MiB

// MaxPathDepth caps how deeply a wiki page may be nested.
const MaxPathDepth = 8

// Store is the data-access object for the wiki. Construction is cheap —
// it just holds the on-disk Layout for path resolution.
type Store struct{ Layout *repostore.Layout }

// New returns a Store that resolves wiki paths against layout.
func New(layout *repostore.Layout) *Store { return &Store{Layout: layout} }

// ValidatePath rejects paths a user shouldn't be able to write: traversal,
// absolute paths, empty segments, non-".md" suffix, or excessive nesting.
// Returns the cleaned (and slash-normalised) path on success.
func ValidatePath(p string) (string, error) {
	if p == "" {
		return "", apperr.Validation("wiki page path is required", nil)
	}
	p = strings.TrimSpace(p)
	// Normalize backslashes — clients on Windows might send "docs\foo.md"
	// without realising. Convert before security checks.
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") {
		return "", apperr.Validation("wiki path must be relative", nil)
	}
	if !strings.HasSuffix(strings.ToLower(p), ".md") {
		return "", apperr.Validation("wiki path must end in .md", nil)
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", apperr.Validation("wiki path must not escape the wiki root", nil)
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) > MaxPathDepth {
		return "", apperr.Validation(fmt.Sprintf("wiki path exceeds max depth of %d", MaxPathDepth), nil)
	}
	for _, seg := range parts {
		if seg == "" || seg == "." || seg == ".." {
			return "", apperr.Validation("wiki path has empty or traversal segment", nil)
		}
		// Reject any control character (NUL, newline, etc).
		for _, r := range seg {
			if r < 0x20 || r == 0x7f {
				return "", apperr.Validation("wiki path contains control characters", nil)
			}
		}
	}
	return cleaned, nil
}

// Exists reports whether the wiki repo has been initialised on disk yet.
func (s *Store) Exists(orgID, projectID uuid.UUID) bool {
	return git.Exists(s.Layout.WikiPath(orgID, projectID))
}

// ensureRepo initialises the wiki bare repo on disk if it doesn't exist yet.
// Idempotent — calling on an existing repo is a no-op. Returns the on-disk
// path either way.
func (s *Store) ensureRepo(orgID, projectID uuid.UUID) (string, error) {
	wikiPath := s.Layout.WikiPath(orgID, projectID)
	if git.Exists(wikiPath) {
		return wikiPath, nil
	}
	if err := git.InitBare(wikiPath, DefaultBranch); err != nil {
		return "", apperr.Wrap(apperr.CodeInternal, "init wiki repo", err)
	}
	return wikiPath, nil
}

// ListPages walks the latest tree on the wiki's default branch and returns
// every .md file. Returns an empty slice (and no error) when the wiki repo
// doesn't exist yet — a fresh project has no pages.
func (s *Store) ListPages(orgID, projectID uuid.UUID) ([]model.WikiPage, error) {
	wikiPath := s.Layout.WikiPath(orgID, projectID)
	if !git.Exists(wikiPath) {
		return []model.WikiPage{}, nil
	}
	headOID, err := git.Resolve(wikiPath, defaultRef)
	if err != nil {
		// No commits yet — fresh InitBare repo. Treat as empty.
		if git.IsNotFound(err) {
			return []model.WikiPage{}, nil
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "resolve wiki head", err)
	}
	out := make([]model.WikiPage, 0)
	if err := walkPages(wikiPath, headOID, "", &out); err != nil {
		return nil, err
	}
	// Stable order regardless of tree iteration internals.
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	// Best-effort: stamp updated_at from the most recent commit on the wiki.
	// Walking every blob through `git log -- <path>` would be wasteful for a
	// list view, so we attribute the head commit's time to every page. The
	// per-page time is exposed via GetPage instead.
	commits, _ := git.Log(wikiPath, headOID, 1)
	if len(commits) > 0 {
		t := commits[0].Author.When
		for i := range out {
			out[i].UpdatedAt = &t
		}
	}
	return out, nil
}

// walkPages recursively collects .md blobs under rootOID. dirPrefix is the
// slash-joined path of the parent tree so we can report full paths.
func walkPages(wikiPath, treeOID, dirPrefix string, out *[]model.WikiPage) error {
	entries, err := git.ReadTree(wikiPath, treeOID)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "read tree", err)
	}
	for _, e := range entries {
		full := e.Name
		if dirPrefix != "" {
			full = dirPrefix + "/" + e.Name
		}
		switch e.Kind {
		case "tree":
			if err := walkPages(wikiPath, e.OID, full, out); err != nil {
				return err
			}
		case "blob":
			if !strings.HasSuffix(strings.ToLower(e.Name), ".md") {
				continue
			}
			blob, berr := git.ReadBlob(wikiPath, e.OID)
			if berr != nil {
				return apperr.Wrap(apperr.CodeInternal, "read blob", berr)
			}
			*out = append(*out, model.WikiPage{
				Path: full,
				Size: int64(len(blob.Data)),
			})
		}
	}
	return nil
}

// GetPage returns the raw bytes + commit OID of a single page.
func (s *Store) GetPage(orgID, projectID uuid.UUID, page string) (raw []byte, commitOID string, err error) {
	clean, verr := ValidatePath(page)
	if verr != nil {
		return nil, "", verr
	}
	wikiPath := s.Layout.WikiPath(orgID, projectID)
	if !git.Exists(wikiPath) {
		return nil, "", apperr.NotFound("wiki page")
	}
	headOID, gerr := git.Resolve(wikiPath, defaultRef)
	if gerr != nil {
		if git.IsNotFound(gerr) {
			return nil, "", apperr.NotFound("wiki page")
		}
		return nil, "", apperr.Wrap(apperr.CodeInternal, "resolve wiki head", gerr)
	}
	blobOID, werr := walkPath(wikiPath, headOID, clean)
	if werr != nil {
		return nil, "", werr
	}
	blob, gerr := git.ReadBlob(wikiPath, blobOID)
	if gerr != nil {
		if git.IsNotFound(gerr) {
			return nil, "", apperr.NotFound("wiki page")
		}
		return nil, "", apperr.Wrap(apperr.CodeInternal, "read blob", gerr)
	}
	return blob.Data, headOID, nil
}

// walkPath walks tree entries under rootOID following slash-separated parts
// and returns the OID of the leaf blob. Returns NotFound when any segment is
// missing or the leaf isn't a blob.
func walkPath(wikiPath, rootOID, p string) (string, error) {
	segments := strings.Split(p, "/")
	curOID := rootOID
	for i, seg := range segments {
		entries, gerr := git.ReadTree(wikiPath, curOID)
		if gerr != nil {
			return "", apperr.Wrap(apperr.CodeInternal, "read tree", gerr)
		}
		var hit *git.TreeEntry
		for j := range entries {
			if entries[j].Name == seg {
				hit = &entries[j]
				break
			}
		}
		if hit == nil {
			return "", apperr.NotFound("wiki page")
		}
		isLast := i == len(segments)-1
		if isLast {
			if hit.Kind != "blob" {
				return "", apperr.New(apperr.CodeBadRequest, "wiki path is not a file")
			}
			return hit.OID, nil
		}
		if hit.Kind != "tree" {
			return "", apperr.NotFound("wiki page")
		}
		curOID = hit.OID
	}
	return "", apperr.NotFound("wiki page")
}

// PutPage writes data at page (validated path), creating the wiki repo on
// the first call. Returns the new commit OID.
func (s *Store) PutPage(orgID, projectID uuid.UUID, page string, data []byte, authorName, authorEmail, message string) (string, error) {
	clean, verr := ValidatePath(page)
	if verr != nil {
		return "", verr
	}
	if len(data) > MaxPageBytes {
		return "", apperr.Validation(fmt.Sprintf("wiki page exceeds %d bytes", MaxPageBytes), nil)
	}
	wikiPath, err := s.ensureRepo(orgID, projectID)
	if err != nil {
		return "", err
	}
	msg := message
	if strings.TrimSpace(msg) == "" {
		msg = "Update " + clean
	}
	sig := git.Author{Name: authorName, Email: authorEmail, When: time.Now().UTC()}
	oid, gerr := git.CommitFile(wikiPath, defaultRef, clean, data, sig, msg)
	if gerr != nil {
		return "", apperr.Wrap(apperr.CodeInternal, "commit wiki page", gerr)
	}
	return oid, nil
}

// DeletePage removes page from the wiki and commits the deletion. Returns
// NotFound when the page never existed.
func (s *Store) DeletePage(orgID, projectID uuid.UUID, page string, authorName, authorEmail, message string) (string, error) {
	clean, verr := ValidatePath(page)
	if verr != nil {
		return "", verr
	}
	wikiPath := s.Layout.WikiPath(orgID, projectID)
	if !git.Exists(wikiPath) {
		return "", apperr.NotFound("wiki page")
	}
	msg := message
	if strings.TrimSpace(msg) == "" {
		msg = "Delete " + clean
	}
	sig := git.Author{Name: authorName, Email: authorEmail, When: time.Now().UTC()}
	oid, gerr := git.DeleteFile(wikiPath, defaultRef, clean, sig, msg)
	if gerr != nil {
		// The C wrapper formats "not found: <path>" for missing entries.
		if git.IsNotFound(gerr) || isWrapperNotFound(gerr) {
			return "", apperr.NotFound("wiki page")
		}
		return "", apperr.Wrap(apperr.CodeInternal, "delete wiki page", gerr)
	}
	return oid, nil
}

func isWrapperNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found:")
}

// History returns the most recent commits on the wiki repo's default branch,
// up to limit (capped to 200). Empty slice if the wiki repo doesn't exist.
func (s *Store) History(orgID, projectID uuid.UUID, limit int) ([]git.Commit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	wikiPath := s.Layout.WikiPath(orgID, projectID)
	if !git.Exists(wikiPath) {
		return []git.Commit{}, nil
	}
	headOID, err := git.Resolve(wikiPath, defaultRef)
	if err != nil {
		if git.IsNotFound(err) {
			return []git.Commit{}, nil
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "resolve wiki head", err)
	}
	commits, gerr := git.Log(wikiPath, headOID, limit)
	if gerr != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "log wiki", gerr)
	}
	return commits, nil
}

// guard against unused imports during incremental edits.
var _ = errors.New
