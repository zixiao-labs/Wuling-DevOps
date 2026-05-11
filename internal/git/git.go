// Package git is the Go-side cgo binding to the C++ libgit2 wrapper.
//
// We deliberately don't expose libgit2 types — the C++ layer in
// wuling_git.cpp marshals everything into a small set of POD structs that
// cross the cgo boundary, and this file converts those into idiomatic Go.
//
// Build dependencies: libgit2 + a C++ compiler. pkg-config provides the
// flags. On macOS: brew install libgit2 pkg-config. On Debian: apt install
// libgit2-dev pkg-config build-essential.
//
// Builds with CGO_ENABLED=0 use the stub in git_stub.go and every entry
// point returns ErrCGOUnsupported.

//go:build cgo

package git

/*
#cgo pkg-config: libgit2
#cgo CXXFLAGS: -std=c++17 -O2 -Wall -Wextra
#cgo LDFLAGS: -lstdc++
#include <stdlib.h>
#include "wuling_git.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

var (
	initOnce sync.Once
	initErr  error
)

// Init brings up libgit2. Safe to call repeatedly.
func Init() error {
	initOnce.Do(func() {
		var cerr C.wg_error
		if rc := C.wg_lib_init(&cerr); rc != 0 {
			initErr = fmt.Errorf("git init: %s (rc=%d)", C.GoString(&cerr.message[0]), int(rc))
		}
	})
	return initErr
}

// Shutdown decrements libgit2's init refcount.
func Shutdown() error {
	var cerr C.wg_error
	if rc := C.wg_lib_shutdown(&cerr); rc < 0 {
		return fmt.Errorf("git shutdown: %s (rc=%d)", C.GoString(&cerr.message[0]), int(rc))
	}
	return nil
}

// errFromC creates a Go error from the wg_error struct populated by C.
func errFromC(rc C.int, cerr C.wg_error, op string) error {
	msg := C.GoString(&cerr.message[0])
	if msg == "" {
		msg = "unknown libgit2 error"
	}
	return fmt.Errorf("%s: %s (rc=%d)", op, msg, int(rc))
}

// InitBare creates a fresh bare repository at path with the given default branch.
func InitBare(path, defaultBranch string) error {
	if err := Init(); err != nil {
		return err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cBranch := C.CString(defaultBranch)
	defer C.free(unsafe.Pointer(cBranch))

	var cerr C.wg_error
	if rc := C.wg_repo_init_bare(cPath, cBranch, &cerr); rc != 0 {
		return errFromC(rc, cerr, "init bare")
	}
	return nil
}

// Exists reports whether path is a libgit2-recognized repository.
//
// We initialise libgit2 (idempotently via sync.Once in Init) before probing,
// so that an init failure surfaces as the boolean false plus implicit "not
// initialised" rather than being silently misread as "no repo at this path".
func Exists(path string) bool {
	if err := Init(); err != nil {
		return false
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	return C.wg_repo_exists(cPath) == 1
}

// Ref describes one reference.
type Ref struct {
	Name     string `json:"name"`
	OID      string `json:"oid"`
	IsBranch bool   `json:"is_branch"`
	IsTag    bool   `json:"is_tag"`
}

// ListRefs returns all refs in the repo.
func ListRefs(path string) ([]Ref, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var list C.wg_ref_list
	var cerr C.wg_error
	if rc := C.wg_repo_list_refs(cPath, &list, &cerr); rc != 0 {
		return nil, errFromC(rc, cerr, "list refs")
	}
	defer C.wg_ref_list_free(&list)

	n := int(list.count)
	if n == 0 {
		return nil, nil
	}
	entries := unsafe.Slice(list.entries, n)
	out := make([]Ref, n)
	for i := 0; i < n; i++ {
		e := entries[i]
		out[i] = Ref{
			Name:     C.GoString(&e.name[0]),
			OID:      C.GoString(&e.target_oid[0]),
			IsBranch: e.is_branch == 1,
			IsTag:    e.is_tag == 1,
		}
	}
	return out, nil
}

// Resolve resolves a revspec ("HEAD", "main", "abc1234", ...) to a 40-char oid.
func Resolve(path, spec string) (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cSpec := C.CString(spec)
	defer C.free(unsafe.Pointer(cSpec))

	var oid [41]C.char
	var cerr C.wg_error
	if rc := C.wg_repo_resolve(cPath, cSpec, &oid[0], &cerr); rc != 0 {
		return "", errFromC(rc, cerr, "resolve")
	}
	return C.GoString(&oid[0]), nil
}

// TreeEntry is one entry in a tree listing.
type TreeEntry struct {
	Name     string `json:"name"`
	OID      string `json:"oid"`
	Filemode uint32 `json:"filemode"`
	Kind     string `json:"kind"` // "blob","tree","submodule","tag","other"
}

func kindString(k C.int) string {
	switch k {
	case 0:
		return "blob"
	case 1:
		return "tree"
	case 2:
		return "submodule"
	case 3:
		return "tag"
	default:
		return "other"
	}
}

// ReadTree returns the entries of a tree (oid may be a tree or commit oid).
func ReadTree(path, oid string) ([]TreeEntry, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cOid := C.CString(oid)
	defer C.free(unsafe.Pointer(cOid))

	var list C.wg_tree_list
	var cerr C.wg_error
	if rc := C.wg_repo_read_tree(cPath, cOid, &list, &cerr); rc != 0 {
		return nil, errFromC(rc, cerr, "read tree")
	}
	defer C.wg_tree_list_free(&list)

	n := int(list.count)
	if n == 0 {
		return nil, nil
	}
	entries := unsafe.Slice(list.entries, n)
	out := make([]TreeEntry, n)
	for i := 0; i < n; i++ {
		e := entries[i]
		out[i] = TreeEntry{
			Name:     C.GoString(&e.name[0]),
			OID:      C.GoString(&e.oid[0]),
			Filemode: uint32(e.filemode),
			Kind:     kindString(e.kind),
		}
	}
	return out, nil
}

// Blob is a blob with metadata.
type Blob struct {
	Data     []byte
	IsBinary bool
}

// ReadBlob fetches blob bytes by oid.
func ReadBlob(path, oid string) (*Blob, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cOid := C.CString(oid)
	defer C.free(unsafe.Pointer(cOid))

	var b C.wg_blob
	var cerr C.wg_error
	if rc := C.wg_repo_read_blob(cPath, cOid, &b, &cerr); rc != 0 {
		return nil, errFromC(rc, cerr, "read blob")
	}
	defer C.wg_blob_free(&b)

	size := int(b.size)
	out := make([]byte, size)
	if size > 0 {
		// copy out of the C buffer immediately so we can free it.
		copy(out, unsafe.Slice((*byte)(unsafe.Pointer(b.data)), size))
	}
	return &Blob{Data: out, IsBinary: b.is_binary == 1}, nil
}

// Signature is an author/committer triple.
type Signature struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	When  time.Time `json:"when"`
}

// Commit summarises a single commit.
type Commit struct {
	OID       string    `json:"oid"`
	TreeOID   string    `json:"tree_oid"`
	Author    Signature `json:"author"`
	Committer Signature `json:"committer"`
	Message   string    `json:"message"`
	Parents   []string  `json:"parents"`
}

// Log walks commits starting at startOID (or HEAD if empty). max==0 -> 50.
func Log(path, startOID string, max int) ([]Commit, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var cStart *C.char
	if startOID != "" {
		cStart = C.CString(startOID)
		defer C.free(unsafe.Pointer(cStart))
	}

	var list C.wg_commit_list
	var cerr C.wg_error
	if rc := C.wg_repo_log(cPath, cStart, C.int(max), &list, &cerr); rc != 0 {
		return nil, errFromC(rc, cerr, "log")
	}
	defer C.wg_commit_list_free(&list)

	n := int(list.count)
	if n == 0 {
		return nil, nil
	}
	commits := unsafe.Slice(list.commits, n)
	out := make([]Commit, n)
	for i := 0; i < n; i++ {
		c := commits[i]
		commit := Commit{
			OID:     C.GoString(&c.oid[0]),
			TreeOID: C.GoString(&c.tree_oid[0]),
			Message: C.GoString(&c.message[0]),
			Author: Signature{
				Name:  C.GoString(&c.author_name[0]),
				Email: C.GoString(&c.author_email[0]),
				When:  time.Unix(int64(c.author_time), 0).UTC(),
			},
			Committer: Signature{
				Name:  C.GoString(&c.committer_name[0]),
				Email: C.GoString(&c.committer_email[0]),
				When:  time.Unix(int64(c.committer_time), 0).UTC(),
			},
		}
		pn := int(c.parent_count)
		if pn > 0 && c.parent_oids != nil {
			parents := unsafe.Slice(c.parent_oids, pn)
			commit.Parents = make([]string, pn)
			for j := 0; j < pn; j++ {
				commit.Parents[j] = C.GoString(parents[j])
			}
		}
		out[i] = commit
	}
	return out, nil
}

// IsNotFound reports whether err looks like a "no such ref/oid" error from
// libgit2; useful for translating to apperr.NotFound.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{"not found", "could not find", "does not exist"} {
		if contains(msg, needle) {
			return true
		}
	}
	return false
}

// IsConflict reports whether err is a merge-conflict error from
// CreateMergeCommit. The C wrapper tags the error message with "conflict:"
// (which may appear anywhere in the formatted text produced by errFromC), so
// IsConflict uses a case-insensitive substring check (see contains) rather
// than a strict prefix test.
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "conflict:")
}

// ----------------------------------------------------------------------------
// Merge / diff helpers (used by the MR domain)
// ----------------------------------------------------------------------------

// Author is the author/committer signature passed when creating commits via
// CreateMergeCommit. When is converted to unix seconds in UTC.
type Author struct {
	Name  string
	Email string
	When  time.Time
}

// DiffEntry is one file's diff between two commit OIDs. Patch is empty
// unless DiffOIDs was called with includePatch=true.
type DiffEntry struct {
	Path      string
	OldPath   string
	Status    string // added | modified | deleted | renamed | copied | typechange | other
	Additions int
	Deletions int
	Patch     string
}

func diffStatusString(k C.int) string {
	switch k {
	case 0:
		return "added"
	case 1:
		return "modified"
	case 2:
		return "deleted"
	case 3:
		return "renamed"
	case 4:
		return "copied"
	case 5:
		return "typechange"
	default:
		return "other"
	}
}

// MergeBase returns the best common ancestor OID of oidA and oidB.
func MergeBase(path, oidA, oidB string) (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cA := C.CString(oidA)
	defer C.free(unsafe.Pointer(cA))
	cB := C.CString(oidB)
	defer C.free(unsafe.Pointer(cB))

	var oid [41]C.char
	var cerr C.wg_error
	if rc := C.wg_repo_merge_base(cPath, cA, cB, &oid[0], &cerr); rc != 0 {
		return "", errFromC(rc, cerr, "merge_base")
	}
	return C.GoString(&oid[0]), nil
}

// DiffOIDs returns the per-file diff between two commit OIDs (base..head).
// When includePatch is true, each entry's Patch field is populated with the
// full unified diff text.
func DiffOIDs(path, baseOID, headOID string, includePatch bool) ([]DiffEntry, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cBase := C.CString(baseOID)
	defer C.free(unsafe.Pointer(cBase))
	cHead := C.CString(headOID)
	defer C.free(unsafe.Pointer(cHead))

	var ip C.int
	if includePatch {
		ip = 1
	}

	var list C.wg_diff_list
	var cerr C.wg_error
	if rc := C.wg_repo_diff_oids(cPath, cBase, cHead, ip, &list, &cerr); rc != 0 {
		return nil, errFromC(rc, cerr, "diff")
	}
	defer C.wg_diff_list_free(&list)

	n := int(list.count)
	if n == 0 {
		return nil, nil
	}
	entries := unsafe.Slice(list.entries, n)
	out := make([]DiffEntry, n)
	for i := 0; i < n; i++ {
		e := entries[i]
		entry := DiffEntry{
			Path:      C.GoString(&e.path[0]),
			OldPath:   C.GoString(&e.old_path[0]),
			Status:    diffStatusString(e.status),
			Additions: int(e.additions),
			Deletions: int(e.deletions),
		}
		if e.patch != nil && e.patch_size > 0 {
			entry.Patch = C.GoStringN(e.patch, C.int(e.patch_size))
		}
		out[i] = entry
	}
	return out, nil
}

// LogRange returns commits reachable from includeOID but not from excludeOID,
// up to max entries (max==0 -> 50). Used by MR commits endpoint to list the
// commits being introduced by a source branch over its target.
func LogRange(path, includeOID, excludeOID string, max int) ([]Commit, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cInc := C.CString(includeOID)
	defer C.free(unsafe.Pointer(cInc))
	var cExc *C.char
	if excludeOID != "" {
		cExc = C.CString(excludeOID)
		defer C.free(unsafe.Pointer(cExc))
	}

	var list C.wg_commit_list
	var cerr C.wg_error
	if rc := C.wg_repo_log_range(cPath, cInc, cExc, C.int(max), &list, &cerr); rc != 0 {
		return nil, errFromC(rc, cerr, "log_range")
	}
	defer C.wg_commit_list_free(&list)

	n := int(list.count)
	if n == 0 {
		return nil, nil
	}
	commits := unsafe.Slice(list.commits, n)
	out := make([]Commit, n)
	for i := 0; i < n; i++ {
		c := commits[i]
		commit := Commit{
			OID:     C.GoString(&c.oid[0]),
			TreeOID: C.GoString(&c.tree_oid[0]),
			Message: C.GoString(&c.message[0]),
			Author: Signature{
				Name:  C.GoString(&c.author_name[0]),
				Email: C.GoString(&c.author_email[0]),
				When:  time.Unix(int64(c.author_time), 0).UTC(),
			},
			Committer: Signature{
				Name:  C.GoString(&c.committer_name[0]),
				Email: C.GoString(&c.committer_email[0]),
				When:  time.Unix(int64(c.committer_time), 0).UTC(),
			},
		}
		pn := int(c.parent_count)
		if pn > 0 && c.parent_oids != nil {
			parents := unsafe.Slice(c.parent_oids, pn)
			commit.Parents = make([]string, pn)
			for j := 0; j < pn; j++ {
				commit.Parents[j] = C.GoString(parents[j])
			}
		}
		out[i] = commit
	}
	return out, nil
}

// FFUpdateRef writes refName (a fully qualified ref like "refs/heads/main")
// to point at newOID. It does NOT verify fast-forwardability — the caller
// must call MergeBase first and confirm the result equals the current ref
// tip. logMsg goes into the reflog (may be empty).
func FFUpdateRef(path, refName, newOID, logMsg string) error {
	if err := Init(); err != nil {
		return err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cRef := C.CString(refName)
	defer C.free(unsafe.Pointer(cRef))
	cOID := C.CString(newOID)
	defer C.free(unsafe.Pointer(cOID))
	cLog := C.CString(logMsg)
	defer C.free(unsafe.Pointer(cLog))

	var cerr C.wg_error
	if rc := C.wg_repo_ff_update_ref(cPath, cRef, cOID, cLog, &cerr); rc != 0 {
		return errFromC(rc, cerr, "ff_update_ref")
	}
	return nil
}

// CreateMergeCommit performs a 3-way merge of the trees at baseOID
// (merge-base), oursOID (target tip), and theirsOID (source tip), and writes
// the resulting commit into targetRef.
//
// When squash is false the new commit has two parents [oursOID, theirsOID]
// (true merge commit). When squash is true the parent list is just [oursOID].
//
// On a merge conflict the returned error satisfies IsConflict — callers
// should map it to apperr.Conflict and leave the MR open.
func CreateMergeCommit(
	path, targetRef, baseOID, oursOID, theirsOID string,
	sig Author,
	message, logMsg string,
	squash bool,
) (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cRef := C.CString(targetRef)
	defer C.free(unsafe.Pointer(cRef))
	cBase := C.CString(baseOID)
	defer C.free(unsafe.Pointer(cBase))
	cOurs := C.CString(oursOID)
	defer C.free(unsafe.Pointer(cOurs))
	cTheirs := C.CString(theirsOID)
	defer C.free(unsafe.Pointer(cTheirs))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))
	cLog := C.CString(logMsg)
	defer C.free(unsafe.Pointer(cLog))

	var sq C.int
	if squash {
		sq = 1
	}

	csig := buildCSignature(sig)

	var oid [41]C.char
	var cerr C.wg_error
	if rc := C.wg_repo_create_merge_commit(
		cPath, cRef, cBase, cOurs, cTheirs, sq,
		&csig, cMsg, cLog,
		&oid[0], &cerr,
	); rc != 0 {
		return "", errFromC(rc, cerr, "create_merge_commit")
	}
	return C.GoString(&oid[0]), nil
}

// ----------------------------------------------------------------------------
// Single-file commits (used by the Wiki domain)
// ----------------------------------------------------------------------------

// CommitFile writes data as a blob at filePath on refName (a fully qualified
// ref like "refs/heads/main") and creates a commit. The commit's parent is
// the previous tip of refName, or empty for the very first commit. The ref
// is created if it does not yet exist. Returns the new commit OID as 40-char
// lowercase hex.
func CommitFile(path, refName, filePath string, data []byte, sig Author, message string) (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cRef := C.CString(refName)
	defer C.free(unsafe.Pointer(cRef))
	cFile := C.CString(filePath)
	defer C.free(unsafe.Pointer(cFile))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))

	// Allocate a stable buffer for blob bytes; data may be nil for an empty
	// blob, in which case we hand C a NUL pointer + zero size which the
	// wrapper accepts.
	var dataPtr *C.char
	var dataLen C.size_t
	if len(data) > 0 {
		buf := C.CBytes(data)
		defer C.free(buf)
		dataPtr = (*C.char)(buf)
		dataLen = C.size_t(len(data))
	}

	csig := buildCSignature(sig)

	var oid [41]C.char
	var cerr C.wg_error
	if rc := C.wg_repo_commit_file(cPath, cRef, cFile, dataPtr, dataLen,
		&csig, cMsg, &oid[0], &cerr); rc != 0 {
		return "", errFromC(rc, cerr, "commit_file")
	}
	return C.GoString(&oid[0]), nil
}

// DeleteFile removes filePath from the tree at refName and creates a commit
// for the deletion. Returns an error matching IsNotFound when filePath did
// not exist in the parent tree.
func DeleteFile(path, refName, filePath string, sig Author, message string) (string, error) {
	if err := Init(); err != nil {
		return "", err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cRef := C.CString(refName)
	defer C.free(unsafe.Pointer(cRef))
	cFile := C.CString(filePath)
	defer C.free(unsafe.Pointer(cFile))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))

	csig := buildCSignature(sig)

	var oid [41]C.char
	var cerr C.wg_error
	if rc := C.wg_repo_delete_file(cPath, cRef, cFile, &csig, cMsg, &oid[0], &cerr); rc != 0 {
		return "", errFromC(rc, cerr, "delete_file")
	}
	return C.GoString(&oid[0]), nil
}

// buildCSignature copies a Go Author into a C wg_signature, truncating
// over-long names/emails the same way safe_copy does on the C side.
func buildCSignature(sig Author) C.wg_signature {
	var csig C.wg_signature
	copyToCBuf := func(dst *C.char, dstLen int, src string) {
		buf := unsafe.Slice((*byte)(unsafe.Pointer(dst)), dstLen)
		n := copy(buf, src)
		if n < dstLen {
			buf[n] = 0
		} else {
			buf[dstLen-1] = 0
		}
	}
	copyToCBuf(&csig.name[0], len(csig.name), sig.Name)
	copyToCBuf(&csig.email[0], len(csig.email), sig.Email)
	when := sig.When
	if when.IsZero() {
		when = time.Now()
	}
	csig.when = C.int64_t(when.Unix())
	return csig
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// guard against unused imports in some build modes.
var _ = errors.New
