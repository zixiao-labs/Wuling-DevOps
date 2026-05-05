// Package git is the Go-side cgo binding to the C++ libgit2 wrapper.
//
// We deliberately don't expose libgit2 types — the C++ layer in
// wuling_git.cpp marshals everything into a small set of POD structs that
// cross the cgo boundary, and this file converts those into idiomatic Go.
//
// Build dependencies: libgit2 + a C++ compiler. pkg-config provides the
// flags. On macOS: brew install libgit2 pkg-config. On Debian: apt install
// libgit2-dev pkg-config build-essential.
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
