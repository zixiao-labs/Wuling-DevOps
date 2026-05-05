// Stub used when the build is configured with CGO_ENABLED=0. The real
// implementation in git.go links against libgit2 via cgo; without cgo the
// package would otherwise fail to build, which would block any consumer
// (e.g. internal/repohttp) from being typechecked or from running tests
// that don't actually exercise libgit2.
//
// Every entry point here mirrors the cgo-side signature and returns
// ErrCGOUnsupported, so callers see a clean, identifiable error instead of
// a link failure.

//go:build !cgo

package git

import (
	"errors"
	"time"
)

// ErrCGOUnsupported is returned by every git function in CGO_ENABLED=0
// builds. Callers can errors.Is against it to distinguish "git wasn't
// linked" from real libgit2 errors.
var ErrCGOUnsupported = errors.New("internal/git: built without cgo; libgit2 backend unavailable")

// Init is a no-op in the stub build; it returns ErrCGOUnsupported so
// startup code that requires git surfaces the unsupported configuration.
func Init() error { return ErrCGOUnsupported }

// Shutdown is a no-op in the stub build.
func Shutdown() error { return nil }

// InitBare reports the build is missing libgit2.
func InitBare(string, string) error { return ErrCGOUnsupported }

// Exists always returns false in the stub build.
func Exists(string) bool { return false }

// Ref describes one reference. Mirrors the cgo type so dependent packages
// compile identically under both build modes.
type Ref struct {
	Name     string `json:"name"`
	OID      string `json:"oid"`
	IsBranch bool   `json:"is_branch"`
	IsTag    bool   `json:"is_tag"`
}

// ListRefs reports the build is missing libgit2.
func ListRefs(string) ([]Ref, error) { return nil, ErrCGOUnsupported }

// Resolve reports the build is missing libgit2.
func Resolve(string, string) (string, error) { return "", ErrCGOUnsupported }

// TreeEntry mirrors the cgo type.
type TreeEntry struct {
	Name     string `json:"name"`
	OID      string `json:"oid"`
	Filemode uint32 `json:"filemode"`
	Kind     string `json:"kind"`
}

// ReadTree reports the build is missing libgit2.
func ReadTree(string, string) ([]TreeEntry, error) { return nil, ErrCGOUnsupported }

// Blob mirrors the cgo type.
type Blob struct {
	Data     []byte
	IsBinary bool
}

// ReadBlob reports the build is missing libgit2.
func ReadBlob(string, string) (*Blob, error) { return nil, ErrCGOUnsupported }

// Signature mirrors the cgo type.
type Signature struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	When  time.Time `json:"when"`
}

// Commit mirrors the cgo type.
type Commit struct {
	OID       string    `json:"oid"`
	TreeOID   string    `json:"tree_oid"`
	Author    Signature `json:"author"`
	Committer Signature `json:"committer"`
	Message   string    `json:"message"`
	Parents   []string  `json:"parents"`
}

// Log reports the build is missing libgit2.
func Log(string, string, int) ([]Commit, error) { return nil, ErrCGOUnsupported }

// IsNotFound returns false in the stub build — without libgit2, callers
// never see a real "not found" error, only ErrCGOUnsupported.
func IsNotFound(error) bool { return false }
