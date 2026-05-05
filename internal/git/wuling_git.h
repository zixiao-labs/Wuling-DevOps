// wuling_git.h — C ABI exposed by the C++ libgit2 wrapper.
//
// Design:
//   - All entry points return 0 on success and a negative int on error.
//   - On error, an out-parameter `wg_error*` is filled with code+message, so
//     the Go side never has to call a separate "last error" function and the
//     C++ side never holds thread-local error state.
//   - All buffers we hand back to Go must be freed via the matching wg_*_free
//     function. cgo manages pinning during a single call; persisted handles
//     must be explicitly released.
//   - Strings are UTF-8, NUL-terminated.
//
// The implementation file (wuling_git.cpp) wraps libgit2's C API in C++ scopes
// (RAII) so we never leak handles on the error path.
#ifndef WULING_GIT_H
#define WULING_GIT_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// wg_error carries a human-readable message back to Go; keep the buffer fixed
// so allocation rules stay simple.
typedef struct wg_error {
    int  code;
    char message[512];
} wg_error;

// Library lifecycle. Safe to call wg_lib_init multiple times; reference
// counted internally by libgit2.
int wg_lib_init(wg_error* err);
int wg_lib_shutdown(wg_error* err);

// Initialise an empty bare repository at `path`. `default_branch` (e.g. "main")
// is written to HEAD as `ref: refs/heads/<branch>`.
int wg_repo_init_bare(const char* path, const char* default_branch, wg_error* err);

// Returns 1 if the path looks like a bare or non-bare repo, 0 otherwise.
int wg_repo_exists(const char* path);

// ------------------------------------------------------------------
// Refs
// ------------------------------------------------------------------

// wg_ref_entry: one entry in a list of refs. `name` is e.g. "refs/heads/main"
// and `target_oid` is a 40-char lowercase hex sha (peeled for tags).
typedef struct wg_ref_entry {
    char name[256];
    char target_oid[41];
    int  is_branch; // 1 if name starts with refs/heads/
    int  is_tag;    // 1 if name starts with refs/tags/
} wg_ref_entry;

typedef struct wg_ref_list {
    int           count;
    wg_ref_entry* entries;
} wg_ref_list;

int  wg_repo_list_refs(const char* path, wg_ref_list* out, wg_error* err);
void wg_ref_list_free(wg_ref_list* list);

// Resolve a revspec (sha, ref name, "HEAD", "main", etc.) to a 40-char oid.
int wg_repo_resolve(const char* path, const char* spec, char oid_out[41], wg_error* err);

// ------------------------------------------------------------------
// Trees & blobs
// ------------------------------------------------------------------

typedef struct wg_tree_entry {
    char     name[256];
    char     oid[41];
    uint32_t filemode;
    int      kind; // 0=blob, 1=tree, 2=commit (submodule), 3=tag, 4=other
} wg_tree_entry;

typedef struct wg_tree_list {
    int            count;
    wg_tree_entry* entries;
} wg_tree_list;

int  wg_repo_read_tree(const char* path, const char* tree_oid, wg_tree_list* out, wg_error* err);
void wg_tree_list_free(wg_tree_list* list);

// Reads a blob into a heap buffer. Caller must wg_blob_free the data.
typedef struct wg_blob {
    char*  data;
    size_t size;
    int    is_binary; // 1 if libgit2 considers it binary
} wg_blob;

int  wg_repo_read_blob(const char* path, const char* blob_oid, wg_blob* out, wg_error* err);
void wg_blob_free(wg_blob* blob);

// ------------------------------------------------------------------
// Commits
// ------------------------------------------------------------------

typedef struct wg_commit {
    char     oid[41];
    char     tree_oid[41];
    char     author_name[256];
    char     author_email[256];
    int64_t  author_time;
    char     committer_name[256];
    char     committer_email[256];
    int64_t  committer_time;
    char     message[4096];
    char**   parent_oids; // each 40-char hex sha + NUL
    int      parent_count;
} wg_commit;

typedef struct wg_commit_list {
    int        count;
    wg_commit* commits;
} wg_commit_list;

// Walk commits starting from `start_oid` (or HEAD if start_oid is NULL),
// returning at most max_count entries in topological-then-time order.
int  wg_repo_log(const char* path, const char* start_oid, int max_count, wg_commit_list* out, wg_error* err);
void wg_commit_list_free(wg_commit_list* list);

#ifdef __cplusplus
} // extern "C"
#endif

#endif // WULING_GIT_H
