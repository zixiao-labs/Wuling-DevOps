// Package repohttp wires the repo HTTP handlers: CRUD plus the read-only
// inspection endpoints (refs/tree/blob/commits) which delegate to the libgit2
// wrapper in internal/git.
package repohttp

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires repo handlers.
type Handler struct {
	Store    *userstore.Store
	Layout   *repostore.Layout
	Verifier *auth.Verifier
}

// Permission is the access level required by a resolveAndCheck call.
type Permission int

const (
	PermRead Permission = iota
	PermWrite
)

// Mount registers routes under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	r.Route("/orgs/{org_slug}/projects/{project_slug}/repos", func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Route("/{repo_slug}", func(r chi.Router) {
			r.Get("/", h.get)
			r.Get("/refs", h.listRefs)
			r.Get("/commits", h.listCommits)
			r.Get("/tree", h.readTree)
			r.Get("/blob", h.readBlob)
		})
	})
}

type createRepoReq struct {
	Slug          string `json:"slug"           validate:"required,min=2,max=64,alphanumdash"`
	DisplayName   string `json:"display_name"   validate:"max=128"`
	Description   string `json:"description"    validate:"max=512"`
	DefaultBranch string `json:"default_branch" validate:"omitempty,min=1,max=64"`
	Visibility    string `json:"visibility"     validate:"omitempty,oneof=private internal public"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	org, err := h.Store.GetOrgBySlug(r.Context(), chi.URLParam(r, "org_slug"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	role, err := h.Store.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if role == "" {
		httpapi.RenderError(w, r, apperr.NotFound("org"))
		return
	}
	project, err := h.Store.GetProjectBySlug(r.Context(), org.ID, chi.URLParam(r, "project_slug"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	repos, err := h.Store.ListRepos(r.Context(), project.ID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	org, err := h.Store.GetOrgBySlug(r.Context(), chi.URLParam(r, "org_slug"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	role, err := h.Store.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// Empty role means the caller isn't a member of this org. Don't leak the
	// org's existence — mirror the read paths and the orghttp.createProject
	// handler by returning 404 instead of 403.
	if role == "" {
		httpapi.RenderError(w, r, apperr.NotFound("org"))
		return
	}
	project, err := h.Store.GetProjectBySlug(r.Context(), org.ID, chi.URLParam(r, "project_slug"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createRepoReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	repo, err := h.Store.CreateRepo(r.Context(), userstore.CreateRepoParams{
		ProjectID:     project.ID,
		Slug:          strings.TrimSpace(req.Slug),
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		DefaultBranch: req.DefaultBranch,
		Visibility:    req.Visibility,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	path := h.Layout.Path(org.ID, project.ID, repo.ID)
	if gerr := git.InitBare(path, repo.DefaultBranch); gerr != nil {
		// Roll back the DB row so we don't leave an orphaned repo record
		// pointing at a non-existent on-disk repository. Best-effort: log
		// the cleanup error but surface the original InitBare failure.
		if derr := h.Store.DeleteRepo(r.Context(), repo.ID); derr != nil {
			httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal,
				"init bare repo (and rollback failed)", gerr))
			return
		}
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "init bare repo", gerr))
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, repo)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	repo, _, _, err := h.resolveAndCheck(r, PermRead)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, repo)
}

func (h *Handler) listRefs(w http.ResponseWriter, r *http.Request) {
	repo, projectID, orgID, err := h.resolveAndCheck(r, PermRead)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	path := h.Layout.Path(orgID, projectID, repo.ID)
	refs, gerr := git.ListRefs(path)
	if gerr != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "list refs", gerr))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"refs": refs})
}

func (h *Handler) listCommits(w http.ResponseWriter, r *http.Request) {
	repo, projectID, orgID, err := h.resolveAndCheck(r, PermRead)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	path := h.Layout.Path(orgID, projectID, repo.ID)
	q := r.URL.Query()

	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, perr := strconv.Atoi(l); perr == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	startSpec := q.Get("ref")
	if startSpec == "" {
		startSpec = repo.DefaultBranch
	}

	if repo.IsEmpty {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"commits": []any{}})
		return
	}

	startOID := ""
	if startSpec != "" {
		oid, gerr := git.Resolve(path, startSpec)
		if gerr != nil {
			if git.IsNotFound(gerr) {
				httpapi.RenderError(w, r, apperr.NotFound("ref"))
				return
			}
			httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "resolve", gerr))
			return
		}
		startOID = oid
	}
	commits, gerr := git.Log(path, startOID, limit)
	if gerr != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "log", gerr))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"commits": commits})
}

func (h *Handler) readTree(w http.ResponseWriter, r *http.Request) {
	repo, projectID, orgID, err := h.resolveAndCheck(r, PermRead)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	path := h.Layout.Path(orgID, projectID, repo.ID)
	q := r.URL.Query()
	spec := q.Get("ref")
	if spec == "" {
		spec = repo.DefaultBranch
	}
	if repo.IsEmpty {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"oid": "", "entries": []any{}})
		return
	}
	oid, gerr := git.Resolve(path, spec)
	if gerr != nil {
		if git.IsNotFound(gerr) {
			httpapi.RenderError(w, r, apperr.NotFound("ref"))
			return
		}
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "resolve", gerr))
		return
	}

	subPath, perr := url.PathUnescape(q.Get("path"))
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid url-encoded path"))
		return
	}
	if subPath != "" {
		// walk to the subtree
		walkedOID, werr := walkPath(path, oid, subPath, "tree")
		if werr != nil {
			httpapi.RenderError(w, r, werr)
			return
		}
		oid = walkedOID
	}

	entries, gerr := git.ReadTree(path, oid)
	if gerr != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "read tree", gerr))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"oid":     oid,
		"entries": entries,
	})
}

func (h *Handler) readBlob(w http.ResponseWriter, r *http.Request) {
	repo, projectID, orgID, err := h.resolveAndCheck(r, PermRead)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	path := h.Layout.Path(orgID, projectID, repo.ID)
	q := r.URL.Query()
	oidStr := q.Get("oid")
	if oidStr == "" {
		spec := q.Get("ref")
		if spec == "" {
			spec = repo.DefaultBranch
		}
		filePath, perr := url.PathUnescape(q.Get("path"))
		if perr != nil || filePath == "" {
			httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "specify ?oid=... or ?ref=...&path=..."))
			return
		}
		rootOID, gerr := git.Resolve(path, spec)
		if gerr != nil {
			if git.IsNotFound(gerr) {
				httpapi.RenderError(w, r, apperr.NotFound("ref"))
				return
			}
			httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "resolve", gerr))
			return
		}
		oid, werr := walkPath(path, rootOID, filePath, "blob")
		if werr != nil {
			httpapi.RenderError(w, r, werr)
			return
		}
		oidStr = oid
	}
	blob, gerr := git.ReadBlob(path, oidStr)
	if gerr != nil {
		if git.IsNotFound(gerr) {
			httpapi.RenderError(w, r, apperr.NotFound("blob"))
			return
		}
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "read blob", gerr))
		return
	}

	resp := map[string]any{
		"oid":       oidStr,
		"size":      len(blob.Data),
		"is_binary": blob.IsBinary,
	}
	if blob.IsBinary || !utf8.Valid(blob.Data) {
		resp["encoding"] = "base64"
		resp["content"] = base64.StdEncoding.EncodeToString(blob.Data)
	} else {
		resp["encoding"] = "utf-8"
		resp["content"] = string(blob.Data)
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

// walkPath walks a tree starting at rootOID following slash-separated
// components in filePath and returns the OID of the leaf. wantKind controls
// whether the leaf must be "blob" or "tree".
func walkPath(repoPath, rootOID, filePath, wantKind string) (string, error) {
	parts := splitPath(filePath)
	if len(parts) == 0 {
		return rootOID, nil
	}
	currentOID := rootOID
	for i, part := range parts {
		entries, gerr := git.ReadTree(repoPath, currentOID)
		if gerr != nil {
			return "", apperr.Wrap(apperr.CodeInternal, "read tree", gerr)
		}
		var found *git.TreeEntry
		for j := range entries {
			if entries[j].Name == part {
				found = &entries[j]
				break
			}
		}
		if found == nil {
			return "", apperr.NotFound("path")
		}
		isLast := i == len(parts)-1
		if isLast {
			if wantKind != "" && found.Kind != wantKind {
				return "", apperr.New(apperr.CodeBadRequest, "path is not a "+wantKind)
			}
			return found.OID, nil
		}
		if found.Kind != "tree" {
			return "", apperr.New(apperr.CodeBadRequest, "intermediate path is not a directory")
		}
		currentOID = found.OID
	}
	return "", apperr.New(apperr.CodeBadRequest, "empty path")
}

func splitPath(p string) []string {
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// resolveAndCheck looks up the repo by URL params and authorizes the caller.
// Returns (repo, projectID, orgID, err).
func (h *Handler) resolveAndCheck(r *http.Request, perm Permission) (*model.Repo, uuid.UUID, uuid.UUID, error) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		return nil, uuid.Nil, uuid.Nil, err
	}
	repo, projectID, orgID, err := h.Store.ResolveRepoPath(
		r.Context(),
		chi.URLParam(r, "org_slug"),
		chi.URLParam(r, "project_slug"),
		chi.URLParam(r, "repo_slug"),
	)
	if err != nil {
		return nil, uuid.Nil, uuid.Nil, err
	}
	role, err := h.Store.MemberRole(r.Context(), orgID, id.UserID)
	if err != nil {
		return nil, uuid.Nil, uuid.Nil, err
	}
	switch perm {
	case PermRead:
		if role == "" && repo.Visibility != "public" {
			return nil, uuid.Nil, uuid.Nil, apperr.NotFound("repo")
		}
	case PermWrite:
		if role == "" {
			return nil, uuid.Nil, uuid.Nil, apperr.Forbidden("not a member")
		}
	}
	return repo, projectID, orgID, nil
}
