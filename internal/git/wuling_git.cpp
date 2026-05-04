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

        git_oid resolved;
        bool have_oid = false;
        if (git_reference_type(ref) == GIT_REFERENCE_DIRECT) {
            const git_oid* oid = git_reference_target(ref);
            if (oid) { resolved = *oid; have_oid = true; }
        } else {
            git_reference* peeled = nullptr;
            if (git_reference_resolve(&peeled, ref) == 0) {
                const git_oid* oid = git_reference_target(peeled);
                if (oid) { resolved = *oid; have_oid = true; }
                git_reference_free(peeled);
            }
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

    git_revwalk* walk = nullptr;
    if (int rc = git_revwalk_new(&walk, r.get()); rc < 0) {
        fill_err(err, rc, "revwalk_new"); return rc;
    }
    git_revwalk_sorting(walk, GIT_SORT_TOPOLOGICAL | GIT_SORT_TIME);

    if (start_oid_hex && start_oid_hex[0] != '\0') {
        git_oid oid;
        if (int rc = git_oid_fromstr(&oid, start_oid_hex); rc < 0) {
            git_revwalk_free(walk);
            fill_err(err, rc, "bad start oid"); return rc;
        }
        if (int rc = git_revwalk_push(walk, &oid); rc < 0) {
            git_revwalk_free(walk);
            fill_err(err, rc, "revwalk_push"); return rc;
        }
    } else {
        if (int rc = git_revwalk_push_head(walk); rc < 0) {
            git_revwalk_free(walk);
            fill_err(err, rc, "revwalk_push_head"); return rc;
        }
    }

    std::vector<wg_commit> commits;
    commits.reserve(max_count);
    git_oid id;
    while (commits.size() < (size_t)max_count && git_revwalk_next(&id, walk) == 0) {
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
                fill_err(err, -1, "oom"); return -1;
            }
            for (unsigned int i = 0; i < pn; ++i) {
                const git_oid* poid = git_commit_parent_id(c, i);
                wc.parent_oids[i] = static_cast<char*>(std::malloc(41));
                if (poid) oid_to_hex(poid, wc.parent_oids[i]);
                else wc.parent_oids[i][0] = '\0';
            }
        }
        commits.push_back(wc);
        git_commit_free(c);
    }
    git_revwalk_free(walk);

    if (commits.empty()) return 0;
    out->count = static_cast<int>(commits.size());
    out->commits = static_cast<wg_commit*>(std::calloc(commits.size(), sizeof(wg_commit)));
    if (!out->commits) { fill_err(err, -1, "oom"); return -1; }
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

} // extern "C"
