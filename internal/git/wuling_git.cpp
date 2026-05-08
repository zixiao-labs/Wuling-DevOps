// wuling_git.cpp — thin C++ wrapper over libgit2.
//
// The wrapper exists for two reasons:
//   1) RAII for libgit2 handles, so error paths can't leak.
//   2) A narrow C ABI tailored to the Go side, instead of fighting cgo with
//      libgit2's ~30 distinct struct types.
//
// libgit2 is itself C; we use its C API and add C++ on top. No C++ types
// cross the ABI boundary.

#include "wuling_git.h"

#include <git2.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

namespace {

// fill_err copies a libgit2 error into our struct.
void fill_err(wg_error* err, int rc, const char* fallback) {
    if (!err) return;
    err->code = rc;
    const git_error* ge = git_error_last();
    const char* msg = (ge && ge->message) ? ge->message : fallback;
    if (!msg) msg = "unknown libgit2 error";
    std::snprintf(err->message, sizeof(err->message), "%s", msg);
}

// safe_copy copies up to dst_size-1 bytes and NUL-terminates.
void safe_copy(char* dst, size_t dst_size, const char* src) {
    if (dst_size == 0) return;
    if (!src) { dst[0] = '\0'; return; }
    std::strncpy(dst, src, dst_size - 1);
    dst[dst_size - 1] = '\0';
}

// scoped_repo opens a repo and frees it on scope exit.
class scoped_repo {
public:
    scoped_repo() : repo_(nullptr) {}
    ~scoped_repo() { if (repo_) git_repository_free(repo_); }
    scoped_repo(const scoped_repo&) = delete;
    scoped_repo& operator=(const scoped_repo&) = delete;

    int open(const char* path) {
        return git_repository_open(&repo_, path);
    }
    git_repository* get() { return repo_; }
private:
    git_repository* repo_;
};

// scoped_revwalk owns a git_revwalk* and frees it on scope exit, mirroring
// scoped_repo. Lets wg_repo_log error paths just `return rc` without
// remembering to free the walker.
class scoped_revwalk {
public:
    scoped_revwalk() : walk_(nullptr) {}
    ~scoped_revwalk() { if (walk_) git_revwalk_free(walk_); }
    scoped_revwalk(const scoped_revwalk&) = delete;
    scoped_revwalk& operator=(const scoped_revwalk&) = delete;

    int new_for(git_repository* repo) {
        return git_revwalk_new(&walk_, repo);
    }
    git_revwalk* get() { return walk_; }
private:
    git_revwalk* walk_;
};

// scoped_commit owns a git_commit* lifetime.
class scoped_commit {
public:
    scoped_commit() : c_(nullptr) {}
    ~scoped_commit() { if (c_) git_commit_free(c_); }
    scoped_commit(const scoped_commit&) = delete;
    scoped_commit& operator=(const scoped_commit&) = delete;
    git_commit** addr() { return &c_; }
    git_commit* get() { return c_; }
private:
    git_commit* c_;
};

// scoped_tree owns a git_tree* lifetime.
class scoped_tree {
public:
    scoped_tree() : t_(nullptr) {}
    ~scoped_tree() { if (t_) git_tree_free(t_); }
    scoped_tree(const scoped_tree&) = delete;
    scoped_tree& operator=(const scoped_tree&) = delete;
    git_tree** addr() { return &t_; }
    git_tree* get() { return t_; }
private:
    git_tree* t_;
};

// scoped_index owns a git_index* lifetime.
class scoped_index {
public:
    scoped_index() : i_(nullptr) {}
    ~scoped_index() { if (i_) git_index_free(i_); }
    scoped_index(const scoped_index&) = delete;
    scoped_index& operator=(const scoped_index&) = delete;
    git_index** addr() { return &i_; }
    git_index* get() { return i_; }
private:
    git_index* i_;
};

// scoped_diff owns a git_diff* lifetime.
class scoped_diff {
public:
    scoped_diff() : d_(nullptr) {}
    ~scoped_diff() { if (d_) git_diff_free(d_); }
    scoped_diff(const scoped_diff&) = delete;
    scoped_diff& operator=(const scoped_diff&) = delete;
    git_diff** addr() { return &d_; }
    git_diff* get() { return d_; }
private:
    git_diff* d_;
};

// scoped_reference owns a git_reference* lifetime.
class scoped_reference {
public:
    scoped_reference() : r_(nullptr) {}
    ~scoped_reference() { if (r_) git_reference_free(r_); }
    scoped_reference(const scoped_reference&) = delete;
    scoped_reference& operator=(const scoped_reference&) = delete;
    git_reference** addr() { return &r_; }
    git_reference* get() { return r_; }
    void reset(git_reference* r) {
        if (r_) git_reference_free(r_);
        r_ = r;
    }
private:
    git_reference* r_;
};

// scoped_signature owns a git_signature* lifetime.
class scoped_signature {
public:
    scoped_signature() : s_(nullptr) {}
    ~scoped_signature() { if (s_) git_signature_free(s_); }
    scoped_signature(const scoped_signature&) = delete;
    scoped_signature& operator=(const scoped_signature&) = delete;
    git_signature** addr() { return &s_; }
    const git_signature* get() const { return s_; }
private:
    git_signature* s_;
};

// free_commit_vector frees the heap allocations attached to each wg_commit
// already pushed into `commits`. Used on the OOM error path of wg_repo_log so
// that previously-built commits don't leak their parent_oids when a later
// allocation fails. The wg_commit POD itself is owned by the std::vector and
// destroyed when the vector goes out of scope.
void free_commit_vector(std::vector<wg_commit>& commits) {
    for (auto& wc : commits) {
        if (wc.parent_oids) {
            for (int j = 0; j < wc.parent_count; ++j) {
                if (wc.parent_oids[j]) std::free(wc.parent_oids[j]);
            }
            std::free(wc.parent_oids);
            wc.parent_oids = nullptr;
        }
    }
}

void oid_to_hex(const git_oid* oid, char out[41]) {
    git_oid_fmt(out, oid);
    out[40] = '\0';
}

} // namespace

extern "C" {

int wg_lib_init(wg_error* err) {
    int rc = git_libgit2_init();
    if (rc < 0) { fill_err(err, rc, "git_libgit2_init failed"); return rc; }
    return 0;
}

int wg_lib_shutdown(wg_error* err) {
    int rc = git_libgit2_shutdown();
    if (rc < 0) { fill_err(err, rc, "git_libgit2_shutdown failed"); return rc; }
    return 0;
}

int wg_repo_init_bare(const char* path, const char* default_branch, wg_error* err) {
    git_repository_init_options opts;
    if (int rc = git_repository_init_options_init(&opts, GIT_REPOSITORY_INIT_OPTIONS_VERSION); rc < 0) {
        fill_err(err, rc, "init options"); return rc;
    }
    opts.flags = GIT_REPOSITORY_INIT_MKPATH | GIT_REPOSITORY_INIT_BARE | GIT_REPOSITORY_INIT_NO_REINIT;
    if (default_branch && default_branch[0] != '\0') {
        opts.initial_head = default_branch;
    }
    git_repository* repo = nullptr;
    int rc = git_repository_init_ext(&repo, path, &opts);
    if (rc < 0) {
        fill_err(err, rc, "git_repository_init_ext failed");
        return rc;
    }
    git_repository_free(repo);
    return 0;
}

int wg_repo_exists(const char* path) {
    git_repository* repo = nullptr;
    int rc = git_repository_open(&repo, path);
    if (rc < 0) return 0;
    git_repository_free(repo);
    return 1;
}

int wg_repo_list_refs(const char* path, wg_ref_list* out, wg_error* err) {
    if (!out) return -1;
    out->count = 0;
    out->entries = nullptr;

    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    git_strarray names;
    if (int rc = git_reference_list(&names, r.get()); rc < 0) {
        fill_err(err, rc, "git_reference_list");
        return rc;
    }

    std::vector<wg_ref_entry> entries;
    entries.reserve(names.count);

    for (size_t i = 0; i < names.count; ++i) {
        const char* name = names.strings[i];
        git_reference* ref = nullptr;
        if (git_reference_lookup(&ref, r.get(), name) < 0) continue;

        // Peel through symbolic refs and annotated tag objects to a commit so
        // target_oid always points at a commit (matching wg_ref_entry's
        // contract documented in wuling_git.h: "peeled for tags").
        git_oid resolved;
        bool have_oid = false;
        git_object* peeled_obj = nullptr;
        if (git_reference_peel(&peeled_obj, ref, GIT_OBJECT_COMMIT) == 0) {
            const git_oid* oid = git_object_id(peeled_obj);
            if (oid) { resolved = *oid; have_oid = true; }
            git_object_free(peeled_obj);
        }
        git_reference_free(ref);
        if (!have_oid) continue;

        wg_ref_entry e{};
        safe_copy(e.name, sizeof(e.name), name);
        oid_to_hex(&resolved, e.target_oid);
        e.is_branch = std::strncmp(name, "refs/heads/", 11) == 0 ? 1 : 0;
        e.is_tag    = std::strncmp(name, "refs/tags/", 10) == 0  ? 1 : 0;
        entries.push_back(e);
    }
    git_strarray_dispose(&names);

    if (entries.empty()) {
        out->count = 0;
        out->entries = nullptr;
        return 0;
    }
    out->count = static_cast<int>(entries.size());
    out->entries = static_cast<wg_ref_entry*>(std::calloc(entries.size(), sizeof(wg_ref_entry)));
    if (!out->entries) { fill_err(err, -1, "oom"); return -1; }
    std::memcpy(out->entries, entries.data(), entries.size() * sizeof(wg_ref_entry));
    return 0;
}

void wg_ref_list_free(wg_ref_list* list) {
    if (!list) return;
    if (list->entries) std::free(list->entries);
    list->entries = nullptr;
    list->count = 0;
}

int wg_repo_resolve(const char* path, const char* spec, char oid_out[41], wg_error* err) {
    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }
    git_object* obj = nullptr;
    int rc = git_revparse_single(&obj, r.get(), spec);
    if (rc < 0) {
        fill_err(err, rc, "revparse");
        return rc;
    }
    const git_oid* oid = git_object_id(obj);
    oid_to_hex(oid, oid_out);
    git_object_free(obj);
    return 0;
}

int wg_repo_read_tree(const char* path, const char* tree_oid_hex, wg_tree_list* out, wg_error* err) {
    if (!out) return -1;
    out->count = 0;
    out->entries = nullptr;

    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    git_oid oid;
    if (int rc = git_oid_fromstr(&oid, tree_oid_hex); rc < 0) {
        fill_err(err, rc, "bad oid"); return rc;
    }

    // Accept a commit oid by peeling to its tree.
    git_object* obj = nullptr;
    if (int rc = git_object_lookup(&obj, r.get(), &oid, GIT_OBJECT_ANY); rc < 0) {
        fill_err(err, rc, "object_lookup"); return rc;
    }
    git_tree* tree = nullptr;
    if (git_object_type(obj) == GIT_OBJECT_COMMIT) {
        git_commit* c = reinterpret_cast<git_commit*>(obj);
        if (int rc = git_commit_tree(&tree, c); rc < 0) {
            git_object_free(obj);
            fill_err(err, rc, "commit_tree"); return rc;
        }
        git_object_free(obj);
    } else if (git_object_type(obj) == GIT_OBJECT_TREE) {
        tree = reinterpret_cast<git_tree*>(obj);
    } else {
        git_object_free(obj);
        fill_err(err, -1, "oid is not a tree or commit");
        return -1;
    }

    size_t n = git_tree_entrycount(tree);
    std::vector<wg_tree_entry> entries;
    entries.reserve(n);
    for (size_t i = 0; i < n; ++i) {
        const git_tree_entry* te = git_tree_entry_byindex(tree, i);
        wg_tree_entry e{};
        safe_copy(e.name, sizeof(e.name), git_tree_entry_name(te));
        oid_to_hex(git_tree_entry_id(te), e.oid);
        e.filemode = static_cast<uint32_t>(git_tree_entry_filemode(te));
        switch (git_tree_entry_type(te)) {
            case GIT_OBJECT_BLOB:   e.kind = 0; break;
            case GIT_OBJECT_TREE:   e.kind = 1; break;
            case GIT_OBJECT_COMMIT: e.kind = 2; break;
            case GIT_OBJECT_TAG:    e.kind = 3; break;
            default:                e.kind = 4; break;
        }
        entries.push_back(e);
    }
    git_tree_free(tree);

    if (entries.empty()) return 0;
    out->count = static_cast<int>(entries.size());
    out->entries = static_cast<wg_tree_entry*>(std::calloc(entries.size(), sizeof(wg_tree_entry)));
    if (!out->entries) { fill_err(err, -1, "oom"); return -1; }
    std::memcpy(out->entries, entries.data(), entries.size() * sizeof(wg_tree_entry));
    return 0;
}

void wg_tree_list_free(wg_tree_list* list) {
    if (!list) return;
    if (list->entries) std::free(list->entries);
    list->entries = nullptr;
    list->count = 0;
}

int wg_repo_read_blob(const char* path, const char* blob_oid_hex, wg_blob* out, wg_error* err) {
    if (!out) return -1;
    out->data = nullptr;
    out->size = 0;
    out->is_binary = 0;

    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    git_oid oid;
    if (int rc = git_oid_fromstr(&oid, blob_oid_hex); rc < 0) {
        fill_err(err, rc, "bad oid"); return rc;
    }
    git_blob* blob = nullptr;
    if (int rc = git_blob_lookup(&blob, r.get(), &oid); rc < 0) {
        fill_err(err, rc, "blob_lookup"); return rc;
    }
    git_object_size_t sz = git_blob_rawsize(blob);
    const void* raw = git_blob_rawcontent(blob);
    char* buf = static_cast<char*>(std::malloc(sz > 0 ? sz : 1));
    if (!buf) { git_blob_free(blob); fill_err(err, -1, "oom"); return -1; }
    if (sz > 0) std::memcpy(buf, raw, sz);
    out->data = buf;
    out->size = static_cast<size_t>(sz);
    out->is_binary = git_blob_is_binary(blob) ? 1 : 0;
    git_blob_free(blob);
    return 0;
}

void wg_blob_free(wg_blob* blob) {
    if (!blob) return;
    if (blob->data) std::free(blob->data);
    blob->data = nullptr;
    blob->size = 0;
}

int wg_repo_log(const char* path, const char* start_oid_hex, int max_count, wg_commit_list* out, wg_error* err) {
    if (!out) return -1;
    out->count = 0;
    out->commits = nullptr;
    if (max_count <= 0) max_count = 50;

    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    scoped_revwalk walk;
    if (int rc = walk.new_for(r.get()); rc < 0) {
        fill_err(err, rc, "revwalk_new"); return rc;
    }
    git_revwalk_sorting(walk.get(), GIT_SORT_TOPOLOGICAL | GIT_SORT_TIME);

    if (start_oid_hex && start_oid_hex[0] != '\0') {
        git_oid oid;
        if (int rc = git_oid_fromstr(&oid, start_oid_hex); rc < 0) {
            fill_err(err, rc, "bad start oid"); return rc;
        }
        if (int rc = git_revwalk_push(walk.get(), &oid); rc < 0) {
            fill_err(err, rc, "revwalk_push"); return rc;
        }
    } else {
        if (int rc = git_revwalk_push_head(walk.get()); rc < 0) {
            fill_err(err, rc, "revwalk_push_head"); return rc;
        }
    }

    std::vector<wg_commit> commits;
    commits.reserve(max_count);
    git_oid id;
    while (commits.size() < (size_t)max_count && git_revwalk_next(&id, walk.get()) == 0) {
        git_commit* c = nullptr;
        if (git_commit_lookup(&c, r.get(), &id) < 0) continue;

        wg_commit wc{};
        oid_to_hex(&id, wc.oid);
        oid_to_hex(git_commit_tree_id(c), wc.tree_oid);
        const git_signature* a = git_commit_author(c);
        const git_signature* co = git_commit_committer(c);
        if (a) {
            safe_copy(wc.author_name, sizeof(wc.author_name), a->name);
            safe_copy(wc.author_email, sizeof(wc.author_email), a->email);
            wc.author_time = (int64_t)a->when.time;
        }
        if (co) {
            safe_copy(wc.committer_name, sizeof(wc.committer_name), co->name);
            safe_copy(wc.committer_email, sizeof(wc.committer_email), co->email);
            wc.committer_time = (int64_t)co->when.time;
        }
        safe_copy(wc.message, sizeof(wc.message), git_commit_message(c));

        unsigned int pn = git_commit_parentcount(c);
        wc.parent_count = static_cast<int>(pn);
        if (pn > 0) {
            wc.parent_oids = static_cast<char**>(std::calloc(pn, sizeof(char*)));
            if (!wc.parent_oids) {
                git_commit_free(c);
                free_commit_vector(commits);
                fill_err(err, -1, "oom"); return -1;
            }
            for (unsigned int i = 0; i < pn; ++i) {
                const git_oid* poid = git_commit_parent_id(c, i);
                wc.parent_oids[i] = static_cast<char*>(std::malloc(41));
                if (!wc.parent_oids[i]) {
                    // Roll back the partially-built parent_oids array so the
                    // caller's wg_commit_list_free isn't asked to walk past a
                    // NULL into uninitialised slots.
                    for (unsigned int j = 0; j < i; ++j) {
                        std::free(wc.parent_oids[j]);
                    }
                    std::free(wc.parent_oids);
                    wc.parent_oids = nullptr;
                    git_commit_free(c);
                    free_commit_vector(commits);
                    fill_err(err, -1, "oom");
                    return -1;
                }
                if (poid) oid_to_hex(poid, wc.parent_oids[i]);
                else wc.parent_oids[i][0] = '\0';
            }
        }
        commits.push_back(wc);
        git_commit_free(c);
    }

    if (commits.empty()) return 0;
    out->count = static_cast<int>(commits.size());
    out->commits = static_cast<wg_commit*>(std::calloc(commits.size(), sizeof(wg_commit)));
    if (!out->commits) {
        free_commit_vector(commits);
        fill_err(err, -1, "oom"); return -1;
    }
    std::memcpy(out->commits, commits.data(), commits.size() * sizeof(wg_commit));
    return 0;
}

void wg_commit_list_free(wg_commit_list* list) {
    if (!list) return;
    for (int i = 0; i < list->count; ++i) {
        wg_commit* c = &list->commits[i];
        if (c->parent_oids) {
            for (int j = 0; j < c->parent_count; ++j) {
                if (c->parent_oids[j]) std::free(c->parent_oids[j]);
            }
            std::free(c->parent_oids);
        }
    }
    if (list->commits) std::free(list->commits);
    list->commits = nullptr;
    list->count = 0;
}

// ------------------------------------------------------------------
// log_range / merge_base / diff / FF update / merge commit
// ------------------------------------------------------------------

namespace {

// build_one_wg_commit converts an open git_commit into a wg_commit POD,
// allocating parent_oids on the heap. On OOM, frees what it allocated and
// returns -1; on success returns 0.
int build_one_wg_commit(git_commit* c, const git_oid* id, wg_commit* out) {
    *out = wg_commit{};
    oid_to_hex(id, out->oid);
    oid_to_hex(git_commit_tree_id(c), out->tree_oid);
    const git_signature* a = git_commit_author(c);
    const git_signature* co = git_commit_committer(c);
    if (a) {
        safe_copy(out->author_name, sizeof(out->author_name), a->name);
        safe_copy(out->author_email, sizeof(out->author_email), a->email);
        out->author_time = (int64_t)a->when.time;
    }
    if (co) {
        safe_copy(out->committer_name, sizeof(out->committer_name), co->name);
        safe_copy(out->committer_email, sizeof(out->committer_email), co->email);
        out->committer_time = (int64_t)co->when.time;
    }
    safe_copy(out->message, sizeof(out->message), git_commit_message(c));

    unsigned int pn = git_commit_parentcount(c);
    out->parent_count = static_cast<int>(pn);
    if (pn > 0) {
        out->parent_oids = static_cast<char**>(std::calloc(pn, sizeof(char*)));
        if (!out->parent_oids) return -1;
        for (unsigned int i = 0; i < pn; ++i) {
            const git_oid* poid = git_commit_parent_id(c, i);
            out->parent_oids[i] = static_cast<char*>(std::malloc(41));
            if (!out->parent_oids[i]) {
                for (unsigned int j = 0; j < i; ++j) std::free(out->parent_oids[j]);
                std::free(out->parent_oids);
                out->parent_oids = nullptr;
                return -1;
            }
            if (poid) oid_to_hex(poid, out->parent_oids[i]);
            else out->parent_oids[i][0] = '\0';
        }
    }
    return 0;
}

// status_to_int maps libgit2 GIT_DELTA_* to the small int we hand to Go.
int status_to_int(git_delta_t s) {
    switch (s) {
        case GIT_DELTA_ADDED:      return 0;
        case GIT_DELTA_MODIFIED:   return 1;
        case GIT_DELTA_DELETED:    return 2;
        case GIT_DELTA_RENAMED:    return 3;
        case GIT_DELTA_COPIED:     return 4;
        case GIT_DELTA_TYPECHANGE: return 5;
        default:                   return 6;
    }
}

} // namespace

int wg_repo_log_range(const char* path,
                      const char* include_oid_hex,
                      const char* exclude_oid_hex,
                      int max_count,
                      wg_commit_list* out,
                      wg_error* err) {
    if (!out) return -1;
    out->count = 0;
    out->commits = nullptr;
    if (max_count <= 0) max_count = 50;

    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    scoped_revwalk walk;
    if (int rc = walk.new_for(r.get()); rc < 0) {
        fill_err(err, rc, "revwalk_new"); return rc;
    }
    git_revwalk_sorting(walk.get(), GIT_SORT_TOPOLOGICAL | GIT_SORT_TIME);

    git_oid include_id;
    if (int rc = git_oid_fromstr(&include_id, include_oid_hex); rc < 0) {
        fill_err(err, rc, "bad include oid"); return rc;
    }
    if (int rc = git_revwalk_push(walk.get(), &include_id); rc < 0) {
        fill_err(err, rc, "revwalk_push"); return rc;
    }
    if (exclude_oid_hex && exclude_oid_hex[0] != '\0') {
        git_oid exclude_id;
        if (int rc = git_oid_fromstr(&exclude_id, exclude_oid_hex); rc < 0) {
            fill_err(err, rc, "bad exclude oid"); return rc;
        }
        if (int rc = git_revwalk_hide(walk.get(), &exclude_id); rc < 0) {
            fill_err(err, rc, "revwalk_hide"); return rc;
        }
    }

    std::vector<wg_commit> commits;
    commits.reserve(max_count);
    git_oid id;
    while (commits.size() < (size_t)max_count && git_revwalk_next(&id, walk.get()) == 0) {
        scoped_commit c;
        if (git_commit_lookup(c.addr(), r.get(), &id) < 0) continue;
        wg_commit wc{};
        if (build_one_wg_commit(c.get(), &id, &wc) < 0) {
            free_commit_vector(commits);
            fill_err(err, -1, "oom"); return -1;
        }
        commits.push_back(wc);
    }

    if (commits.empty()) return 0;
    out->count = static_cast<int>(commits.size());
    out->commits = static_cast<wg_commit*>(std::calloc(commits.size(), sizeof(wg_commit)));
    if (!out->commits) {
        free_commit_vector(commits);
        fill_err(err, -1, "oom"); return -1;
    }
    std::memcpy(out->commits, commits.data(), commits.size() * sizeof(wg_commit));
    return 0;
}

int wg_repo_merge_base(const char* path,
                       const char* oid_a_hex,
                       const char* oid_b_hex,
                       char oid_out[41],
                       wg_error* err) {
    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    git_oid a, b;
    if (int rc = git_oid_fromstr(&a, oid_a_hex); rc < 0) { fill_err(err, rc, "bad oid_a"); return rc; }
    if (int rc = git_oid_fromstr(&b, oid_b_hex); rc < 0) { fill_err(err, rc, "bad oid_b"); return rc; }

    git_oid base;
    int rc = git_merge_base(&base, r.get(), &a, &b);
    if (rc < 0) {
        fill_err(err, rc, "merge_base");
        return rc;
    }
    oid_to_hex(&base, oid_out);
    return 0;
}

int wg_repo_diff_oids(const char* path,
                      const char* base_oid_hex,
                      const char* head_oid_hex,
                      int include_patch,
                      wg_diff_list* out,
                      wg_error* err) {
    if (!out) return -1;
    out->count = 0;
    out->entries = nullptr;

    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    git_oid base_id, head_id;
    if (int rc = git_oid_fromstr(&base_id, base_oid_hex); rc < 0) { fill_err(err, rc, "bad base oid"); return rc; }
    if (int rc = git_oid_fromstr(&head_id, head_oid_hex); rc < 0) { fill_err(err, rc, "bad head oid"); return rc; }

    scoped_commit base_c, head_c;
    if (int rc = git_commit_lookup(base_c.addr(), r.get(), &base_id); rc < 0) {
        fill_err(err, rc, "lookup base commit"); return rc;
    }
    if (int rc = git_commit_lookup(head_c.addr(), r.get(), &head_id); rc < 0) {
        fill_err(err, rc, "lookup head commit"); return rc;
    }

    scoped_tree base_t, head_t;
    if (int rc = git_commit_tree(base_t.addr(), base_c.get()); rc < 0) {
        fill_err(err, rc, "base tree"); return rc;
    }
    if (int rc = git_commit_tree(head_t.addr(), head_c.get()); rc < 0) {
        fill_err(err, rc, "head tree"); return rc;
    }

    git_diff_options dopts;
    git_diff_init_options(&dopts, GIT_DIFF_OPTIONS_VERSION);

    scoped_diff diff;
    if (int rc = git_diff_tree_to_tree(diff.addr(), r.get(), base_t.get(), head_t.get(), &dopts); rc < 0) {
        fill_err(err, rc, "diff_tree_to_tree"); return rc;
    }

    // Detect renames so the per-file status reflects them; without this,
    // libgit2 reports a delete + add pair for every rename.
    git_diff_find_options fopts;
    git_diff_find_init_options(&fopts, GIT_DIFF_FIND_OPTIONS_VERSION);
    fopts.flags = GIT_DIFF_FIND_RENAMES | GIT_DIFF_FIND_COPIES;
    (void)git_diff_find_similar(diff.get(), &fopts); // best-effort; ignore rc

    size_t n = git_diff_num_deltas(diff.get());
    std::vector<wg_diff_entry> entries;
    entries.reserve(n);

    for (size_t i = 0; i < n; ++i) {
        git_patch* patch = nullptr;
        if (git_patch_from_diff(&patch, diff.get(), i) < 0) {
            // Skip this delta — partial output is preferable to losing the whole diff.
            continue;
        }
        const git_diff_delta* delta = git_patch_get_delta(patch);

        wg_diff_entry e{};
        e.status = status_to_int(delta->status);
        // For deletions the meaningful path is the old one; new_file.path is
        // typically NULL there, so handle that case before any strcmp on it.
        if (delta->status == GIT_DELTA_DELETED) {
            if (delta->old_file.path) {
                safe_copy(e.path, sizeof(e.path), delta->old_file.path);
            }
        } else {
            if (delta->new_file.path) safe_copy(e.path, sizeof(e.path), delta->new_file.path);
            if (delta->old_file.path && delta->new_file.path &&
                std::strcmp(delta->old_file.path, delta->new_file.path) != 0) {
                safe_copy(e.old_path, sizeof(e.old_path), delta->old_file.path);
            }
        }

        size_t ctx = 0, adds = 0, dels = 0;
        if (git_patch_line_stats(&ctx, &adds, &dels, patch) == 0) {
            e.additions = static_cast<int>(adds);
            e.deletions = static_cast<int>(dels);
        }

        if (include_patch) {
            git_buf buf = GIT_BUF_INIT;
            if (git_patch_to_buf(&buf, patch) == 0 && buf.ptr && buf.size > 0) {
                e.patch = static_cast<char*>(std::malloc(buf.size + 1));
                if (e.patch) {
                    std::memcpy(e.patch, buf.ptr, buf.size);
                    e.patch[buf.size] = '\0';
                    e.patch_size = buf.size;
                }
            }
            git_buf_dispose(&buf);
        }

        entries.push_back(e);
        git_patch_free(patch);
    }

    if (entries.empty()) return 0;
    out->count = static_cast<int>(entries.size());
    out->entries = static_cast<wg_diff_entry*>(std::calloc(entries.size(), sizeof(wg_diff_entry)));
    if (!out->entries) {
        // Free per-entry patches we already allocated.
        for (auto& e : entries) { if (e.patch) std::free(e.patch); }
        fill_err(err, -1, "oom"); return -1;
    }
    std::memcpy(out->entries, entries.data(), entries.size() * sizeof(wg_diff_entry));
    return 0;
}

void wg_diff_list_free(wg_diff_list* list) {
    if (!list) return;
    for (int i = 0; i < list->count; ++i) {
        if (list->entries[i].patch) std::free(list->entries[i].patch);
    }
    if (list->entries) std::free(list->entries);
    list->entries = nullptr;
    list->count = 0;
}

int wg_repo_ff_update_ref(const char* path,
                          const char* ref_name,
                          const char* new_oid_hex,
                          const char* log_msg,
                          wg_error* err) {
    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    git_oid new_oid;
    if (int rc = git_oid_fromstr(&new_oid, new_oid_hex); rc < 0) {
        fill_err(err, rc, "bad new oid"); return rc;
    }

    scoped_reference ref;
    if (int rc = git_reference_lookup(ref.addr(), r.get(), ref_name); rc < 0) {
        fill_err(err, rc, "ref lookup"); return rc;
    }

    git_reference* new_ref = nullptr;
    int rc = git_reference_set_target(&new_ref, ref.get(), &new_oid, log_msg);
    if (rc < 0) {
        fill_err(err, rc, "set_target");
        return rc;
    }
    git_reference_free(new_ref);
    return 0;
}

int wg_repo_create_merge_commit(const char* path,
                                const char* target_ref,
                                const char* base_oid_hex,
                                const char* ours_oid_hex,
                                const char* theirs_oid_hex,
                                int squash,
                                const wg_signature* sig,
                                const char* message,
                                const char* /*log_msg*/, // reserved; libgit2 derives the reflog entry from `message` for the commit and uses a fixed string for the ref move.
                                char oid_out[41],
                                wg_error* err) {
    if (!sig) { fill_err(err, -1, "missing signature"); return -1; }

    scoped_repo r;
    if (int rc = r.open(path); rc < 0) { fill_err(err, rc, "open repo"); return rc; }

    git_oid base_id, ours_id, theirs_id;
    if (int rc = git_oid_fromstr(&base_id,   base_oid_hex);   rc < 0) { fill_err(err, rc, "bad base oid"); return rc; }
    if (int rc = git_oid_fromstr(&ours_id,   ours_oid_hex);   rc < 0) { fill_err(err, rc, "bad ours oid"); return rc; }
    if (int rc = git_oid_fromstr(&theirs_id, theirs_oid_hex); rc < 0) { fill_err(err, rc, "bad theirs oid"); return rc; }

    scoped_commit base_c, ours_c, theirs_c;
    if (int rc = git_commit_lookup(base_c.addr(),   r.get(), &base_id);   rc < 0) { fill_err(err, rc, "lookup base");   return rc; }
    if (int rc = git_commit_lookup(ours_c.addr(),   r.get(), &ours_id);   rc < 0) { fill_err(err, rc, "lookup ours");   return rc; }
    if (int rc = git_commit_lookup(theirs_c.addr(), r.get(), &theirs_id); rc < 0) { fill_err(err, rc, "lookup theirs"); return rc; }

    scoped_tree base_t, ours_t, theirs_t;
    if (int rc = git_commit_tree(base_t.addr(),   base_c.get());   rc < 0) { fill_err(err, rc, "base tree");   return rc; }
    if (int rc = git_commit_tree(ours_t.addr(),   ours_c.get());   rc < 0) { fill_err(err, rc, "ours tree");   return rc; }
    if (int rc = git_commit_tree(theirs_t.addr(), theirs_c.get()); rc < 0) { fill_err(err, rc, "theirs tree"); return rc; }

    git_merge_options mopts;
    git_merge_init_options(&mopts, GIT_MERGE_OPTIONS_VERSION);

    scoped_index idx;
    if (int rc = git_merge_trees(idx.addr(), r.get(), base_t.get(), ours_t.get(), theirs_t.get(), &mopts); rc < 0) {
        fill_err(err, rc, "merge_trees"); return rc;
    }
    if (git_index_has_conflicts(idx.get())) {
        // Prefix "conflict:" so the Go layer can map to apperr.Conflict by
        // string match without a separate enum.
        fill_err(err, -1, "conflict: merge has conflicts; resolve and retry");
        return -1;
    }

    git_oid merged_tree_oid;
    if (int rc = git_index_write_tree_to(&merged_tree_oid, idx.get(), r.get()); rc < 0) {
        fill_err(err, rc, "write_tree"); return rc;
    }

    scoped_tree merged_t;
    if (int rc = git_tree_lookup(merged_t.addr(), r.get(), &merged_tree_oid); rc < 0) {
        fill_err(err, rc, "lookup merged tree"); return rc;
    }

    scoped_signature gsig;
    if (int rc = git_signature_new(gsig.addr(), sig->name, sig->email, (git_time_t)sig->when, 0); rc < 0) {
        fill_err(err, rc, "signature_new"); return rc;
    }

    // Build parent list. Squash = single parent; otherwise [ours, theirs].
    const git_commit* parents[2];
    int parent_count = 0;
    parents[parent_count++] = ours_c.get();
    if (!squash) {
        parents[parent_count++] = theirs_c.get();
    }

    git_oid new_oid;
    int rc = git_commit_create(
        &new_oid, r.get(), target_ref,
        gsig.get(), gsig.get(),
        "UTF-8",
        message ? message : "",
        merged_t.get(),
        parent_count, parents);
    if (rc < 0) {
        fill_err(err, rc, "commit_create");
        return rc;
    }
    oid_to_hex(&new_oid, oid_out);
    return 0;
}

} // extern "C"
