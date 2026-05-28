// Package mrhttp wires HTTP handlers for the Merge Requests domain: open/
// list/get/diff/commits/merge/close/reopen plus comments and reviews.
//
// Routes are mounted under
// "/api/v1/orgs/{org_slug}/projects/{project_slug}/repos/{repo_slug}/merge-requests".
// Authorization mirrors the rest of the platform:
//
//   - Read endpoints: any org member, plus anonymous reads against public repos.
//   - Open MR / comment / review / merge / close / reopen: org member.
//   - PATCH (title/body): the MR author or an org owner/admin.
//
// All libgit2 calls live in this package — mrstore is database-only.
package mrhttp

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/mrstore"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires merge-request handlers.
type Handler struct {
	Users    *userstore.Store
	MRs      *mrstore.Store
	Layout   *repostore.Layout
	Verifier *auth.Verifier
	// OAT resolves OAuth-provider access tokens (wloat_…) so third-party
	// OAuth clients can read/write MRs with a bearer. When nil, OAT-shaped
	// bearers are rejected with the standard 401.
	OAT auth.OATResolver

	// mergeLocks serialises concurrent merges that target the same branch in
	// the same repo, so two MRs landing on refs/heads/main can't race the
	// ref-write and produce a lost-update (we'd write the second merge
	// without acknowledging the first, while MarkMerged still records the
	// stale result). Process-local; sufficient for single-process deploys.
	mergeMu    sync.Mutex
	mergeLocks map[string]*sync.Mutex
}

// Mount registers routes under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	r.Route("/orgs/{org_slug}/projects/{project_slug}/repos/{repo_slug}/merge-requests", func(r chi.Router) {
		r.Use(auth.MiddlewareBearer(auth.BearerResolver{JWT: h.Verifier, OAT: h.OAT}, false))
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Route("/{number}", func(r chi.Router) {
			r.Get("/", h.get)
			r.Patch("/", h.patch)
			r.Get("/diff", h.diff)
			r.Get("/commits", h.commits)
			r.Post("/merge", h.merge)
			r.Post("/close", h.close)
			r.Post("/reopen", h.reopen)
			r.Get("/comments", h.listComments)
			r.Post("/comments", h.createComment)
			r.Get("/reviews", h.listReviews)
			r.Post("/reviews", h.createReview)
		})
	})
}

// ----------------------------------------------------------------------------
// authorization helpers
// ----------------------------------------------------------------------------

// repoCtx is the resolved org/project/repo plus the caller's role and the
// info we need to sign merge commits. Returned by resolveRepo so handlers
// don't repeat themselves.
type repoCtx struct {
	Repo        *model.Repo
	OrgID       uuid.UUID
	ProjectID   uuid.UUID
	UserID      uuid.UUID
	Username    string
	UserEmail   string
	UserDisplay string
	Role        string // legal values are documented in internal/auth/roles.go
	RepoPath    string
}

// resolveRepo loads org/project/repo from URL slugs and resolves the caller's
// membership role. It does NOT enforce read/write permissions itself —
// handlers use requireRead / requireWrite afterwards so they can choose the
// right policy (read paths allow public-repo fall-through, writes don't).
func (h *Handler) resolveRepo(r *http.Request) (*repoCtx, error) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		return nil, err
	}
	// We need the caller's email + display_name to sign merge commits, so
	// fetch the full user record up front rather than peppering DB lookups
	// across handlers.
	user, err := h.Users.GetUserByID(r.Context(), id.UserID)
	if err != nil {
		return nil, err
	}
	repo, projectID, orgID, err := h.Users.ResolveRepoPath(
		r.Context(),
		chi.URLParam(r, "org_slug"),
		chi.URLParam(r, "project_slug"),
		chi.URLParam(r, "repo_slug"),
	)
	if err != nil {
		return nil, err
	}
	role, err := h.Users.MemberRole(r.Context(), orgID, id.UserID)
	if err != nil {
		return nil, err
	}
	return &repoCtx{
		Repo:        repo,
		OrgID:       orgID,
		ProjectID:   projectID,
		UserID:      user.ID,
		Username:    user.Username,
		UserEmail:   user.Email,
		UserDisplay: user.DisplayName,
		Role:        role,
		RepoPath:    h.Layout.Path(orgID, projectID, repo.ID),
	}, nil
}

// requireRead enforces read access. Public repos are readable by any
// authenticated user; private/internal repos require org membership. Returns
// a 404 (not 403) on failure to avoid leaking that the repo exists.
func requireRead(rc *repoCtx) error {
	if !auth.CanReadRepo(rc.Role) && rc.Repo.Visibility != "public" {
		return apperr.NotFound("repo")
	}
	return nil
}

// requireWrite enforces write access. Developers and above can open MRs,
// merge, comment, and review; reporters and guests can only read.
func requireWrite(rc *repoCtx) error {
	if !auth.CanWriteRepo(rc.Role) {
		return apperr.Forbidden("write access required")
	}
	return nil
}

// canAdmin reports whether role grants content-moderation power on the repo
// — editing other people's MRs and comments, force-closing somebody else's
// MR. Maps to the GitLab "Maintainer+" tier.
func canAdmin(role string) bool { return auth.CanModerateContent(role) }

// parseNumber pulls the {number} URL parameter as a positive int64.
func parseNumber(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "number")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 0, apperr.New(apperr.CodeBadRequest, "invalid merge request number")
	}
	return n, nil
}

// ----------------------------------------------------------------------------
// MR CRUD
// ----------------------------------------------------------------------------

type createMRReq struct {
	Title     string `json:"title"      validate:"required,min=1,max=256"`
	Body      string `json:"body"       validate:"max=65536"`
	SourceRef string `json:"source_ref" validate:"required,min=1,max=256"`
	TargetRef string `json:"target_ref" validate:"required,min=1,max=256"`
}

type patchMRReq struct {
	Title *string `json:"title,omitempty" validate:"omitempty,min=1,max=256"`
	Body  *string `json:"body,omitempty"  validate:"omitempty,max=65536"`
}

type mergeReq struct {
	Strategy string `json:"strategy" validate:"required,oneof=ff merge-commit squash"`
	Message  string `json:"message"  validate:"max=65536"`
}

type createCommentReq struct {
	Body string `json:"body" validate:"required,min=1,max=65536"`
}

type createReviewReq struct {
	State string `json:"state" validate:"required,oneof=approved changes_requested commented"`
	Body  string `json:"body"  validate:"max=65536"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireRead(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	q := r.URL.Query()
	f := mrstore.ListMRsFilter{
		State:     strings.ToLower(strings.TrimSpace(q.Get("state"))),
		TargetRef: q.Get("target_ref"),
	}
	if f.State != "" && f.State != "open" && f.State != "merged" && f.State != "closed" {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "state must be open, merged, or closed"))
		return
	}
	if a := strings.TrimSpace(q.Get("author")); a != "" {
		uid, err := h.resolveUserRef(r, a)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		f.AuthorID = uid
	}
	if l := q.Get("limit"); l != "" {
		n, perr := strconv.Atoi(l)
		if perr != nil {
			httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid limit parameter"))
			return
		}
		f.Limit = n
	}
	mrs, err := h.MRs.ListMRs(r.Context(), rc.Repo.ID, f)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"merge_requests": mrs})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireWrite(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createMRReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// MRs target branches only — reject explicit tag refs up-front so we
	// don't try to fast-forward or merge-commit onto a tag.
	if isTagRef(req.SourceRef) || isTagRef(req.TargetRef) {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "source_ref and target_ref must be branches, not tags"))
		return
	}
	// Compare on the qualified form so "main" and "refs/heads/main" don't
	// sneak past the equality check.
	if qualifyBranch(req.SourceRef) == qualifyBranch(req.TargetRef) {
		httpapi.RenderError(w, r, apperr.Validation("source_ref and target_ref must differ", nil))
		return
	}
	if rc.Repo.IsEmpty {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "repo is empty; push some commits before opening a merge request"))
		return
	}

	// Resolve both refs to OIDs. Fully-qualified refs are accepted as-is;
	// short branch names ("main") are resolved by libgit2's revparse.
	sourceOID, err := resolveRefOrNotFound(rc.RepoPath, req.SourceRef, "source_ref")
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	targetOID, err := resolveRefOrNotFound(rc.RepoPath, req.TargetRef, "target_ref")
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if sourceOID == targetOID {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "source and target point at the same commit; nothing to merge"))
		return
	}

	mr, err := h.MRs.CreateMR(r.Context(), mrstore.CreateMRParams{
		RepoID:          rc.Repo.ID,
		ProjectID:       rc.ProjectID,
		AuthorID:        rc.UserID,
		Title:           req.Title,
		Body:            req.Body,
		SourceRef:       qualifyBranch(req.SourceRef),
		TargetRef:       qualifyBranch(req.TargetRef),
		SourceOIDAtOpen: sourceOID,
		TargetOIDAtOpen: targetOID,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, mr)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireRead(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	mr, err := h.MRs.GetMRByNumber(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if mr.RepoID != rc.Repo.ID {
		httpapi.RenderError(w, r, apperr.NotFound("merge request"))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, mr)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireWrite(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	current, err := h.MRs.GetMRByNumber(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if current.RepoID != rc.Repo.ID {
		httpapi.RenderError(w, r, apperr.NotFound("merge request"))
		return
	}
	if !canAdmin(rc.Role) && (current.Author == nil || current.Author.ID != rc.UserID) {
		httpapi.RenderError(w, r, apperr.Forbidden("only the author or an org owner/admin can edit this merge request"))
		return
	}

	var req patchMRReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	mr, err := h.MRs.PatchMR(r.Context(), rc.ProjectID, number, mrstore.PatchMRParams{
		Title: req.Title,
		Body:  req.Body,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, mr)
}

// ----------------------------------------------------------------------------
// Diff and commits (libgit2)
// ----------------------------------------------------------------------------

// pickDiffOIDs returns the (target_oid, source_oid) to diff for a given MR.
// Open MRs re-resolve both refs so the diff reflects the latest pushes;
// merged/closed MRs use the snapshot recorded at open time so the historical
// view is stable even if the source ref is later deleted.
func (h *Handler) pickDiffOIDs(rc *repoCtx, mr *model.MergeRequest) (string, string, error) {
	if mr.State != "open" {
		return mr.TargetOIDAtOpen, mr.SourceOIDAtOpen, nil
	}
	target, err := resolveRefOrNotFound(rc.RepoPath, mr.TargetRef, "target_ref")
	if err != nil {
		return "", "", err
	}
	source, err := resolveRefOrNotFound(rc.RepoPath, mr.SourceRef, "source_ref")
	if err != nil {
		return "", "", err
	}
	return target, source, nil
}

func (h *Handler) diff(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireRead(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	mr, err := h.MRs.GetMRByNumber(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if mr.RepoID != rc.Repo.ID {
		httpapi.RenderError(w, r, apperr.NotFound("merge request"))
		return
	}

	targetOID, sourceOID, err := h.pickDiffOIDs(rc, mr)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	baseOID, gerr := git.MergeBase(rc.RepoPath, targetOID, sourceOID)
	if gerr != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeBadRequest, "no common ancestor between source and target", gerr))
		return
	}

	includePatch := wantPatch(r.URL.Query().Get("include"))
	entries, gerr := git.DiffOIDs(rc.RepoPath, baseOID, sourceOID, includePatch)
	if gerr != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "diff", gerr))
		return
	}

	files := make([]model.MRDiffEntry, len(entries))
	for i, e := range entries {
		files[i] = model.MRDiffEntry{
			Path:      e.Path,
			OldPath:   e.OldPath,
			Status:    e.Status,
			Additions: e.Additions,
			Deletions: e.Deletions,
			Patch:     e.Patch,
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"base_oid":   baseOID,
		"source_oid": sourceOID,
		"target_oid": targetOID,
		"files":      files,
	})
}

func (h *Handler) commits(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireRead(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	mr, err := h.MRs.GetMRByNumber(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if mr.RepoID != rc.Repo.ID {
		httpapi.RenderError(w, r, apperr.NotFound("merge request"))
		return
	}
	targetOID, sourceOID, err := h.pickDiffOIDs(rc, mr)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, perr := strconv.Atoi(l); perr == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	commits, gerr := git.LogRange(rc.RepoPath, sourceOID, targetOID, limit)
	if gerr != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "log_range", gerr))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"commits": commits})
}

// ----------------------------------------------------------------------------
// Merge / close / reopen
// ----------------------------------------------------------------------------

func (h *Handler) merge(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireWrite(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	var req mergeReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	mr, err := h.MRs.GetMRByNumber(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if mr.RepoID != rc.Repo.ID {
		httpapi.RenderError(w, r, apperr.NotFound("merge request"))
		return
	}
	if mr.State != "open" {
		httpapi.RenderError(w, r, apperr.Conflict("merge request is not open (current state: "+mr.State+")"))
		return
	}

	// Serialise concurrent merges into the same target ref. Without this two
	// merges resolving the same target tip can both call FFUpdateRef
	// non-atomically and lose one of the resulting commits, while MarkMerged
	// still records both as merged.
	unlock := h.lockTarget(rc.Repo.ID, mr.TargetRef)
	defer unlock()

	// Re-resolve both refs to current tips. We never trust the snapshot OIDs
	// for the actual ref-write — other pushes may have advanced either side
	// since the MR was opened.
	targetOID, err := resolveRefOrNotFound(rc.RepoPath, mr.TargetRef, "target_ref")
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	sourceOID, err := resolveRefOrNotFound(rc.RepoPath, mr.SourceRef, "source_ref")
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	baseOID, gerr := git.MergeBase(rc.RepoPath, targetOID, sourceOID)
	if gerr != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeBadRequest, "no common ancestor between source and target", gerr))
		return
	}

	sig := git.Author{
		Name:  firstNonEmpty(rc.UserDisplay, rc.Username),
		Email: firstNonEmpty(rc.UserEmail, rc.Username+"@users.wuling.local"),
		When:  time.Now(),
	}
	logMsg := fmt.Sprintf("merge MR #%d (%s) by %s", mr.Number, req.Strategy, rc.Username)

	var mergeOID string
	switch req.Strategy {
	case "ff":
		// FF requires the target to be an ancestor of the source — i.e. the
		// merge base must equal the current target tip. If not, the user
		// needs to pick merge-commit or squash.
		if baseOID != targetOID {
			httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest,
				"branches are not fast-forwardable; choose merge-commit or squash"))
			return
		}
		if gerr := git.FFUpdateRef(rc.RepoPath, mr.TargetRef, sourceOID, logMsg); gerr != nil {
			httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "ff update", gerr))
			return
		}
		mergeOID = sourceOID

	case "merge-commit":
		msg := req.Message
		if msg == "" {
			msg = fmt.Sprintf("Merge MR #%d: %s\n", mr.Number, mr.Title)
		}
		oid, gerr := git.CreateMergeCommit(
			rc.RepoPath, mr.TargetRef, baseOID, targetOID, sourceOID,
			sig, msg, logMsg, false /*squash*/)
		if gerr != nil {
			if git.IsConflict(gerr) {
				httpapi.RenderError(w, r, apperr.Conflict("merge has conflicts; resolve and retry"))
				return
			}
			httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "merge commit", gerr))
			return
		}
		mergeOID = oid

	case "squash":
		msg := req.Message
		if msg == "" {
			msg = fmt.Sprintf("Squash merge of MR #%d: %s\n", mr.Number, mr.Title)
		}
		oid, gerr := git.CreateMergeCommit(
			rc.RepoPath, mr.TargetRef, baseOID, targetOID, sourceOID,
			sig, msg, logMsg, true /*squash*/)
		if gerr != nil {
			if git.IsConflict(gerr) {
				httpapi.RenderError(w, r, apperr.Conflict("merge has conflicts; resolve and retry"))
				return
			}
			httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "squash", gerr))
			return
		}
		mergeOID = oid

	default:
		// Validator catches this — defensive only.
		httpapi.RenderError(w, r, apperr.Validation("unknown strategy", nil))
		return
	}

	updated, err := h.MRs.MarkMerged(r.Context(), rc.ProjectID, number, req.Strategy, mergeOID, rc.UserID)
	if err != nil {
		// The ref already moved on disk; the DB row didn't. Surface the
		// error so an operator can reconcile, but don't pretend the merge
		// didn't happen.
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "mark merged (ref already updated on disk)", err))
		return
	}

	// Best-effort book-keeping; failures here don't undo the merge.
	_ = h.Users.MarkRepoNotEmpty(r.Context(), rc.Repo.ID)
	if size, sErr := repostore.DirSize(rc.RepoPath); sErr == nil {
		_ = h.Users.UpdateRepoSize(r.Context(), rc.Repo.ID, size)
	}

	httpapi.WriteJSON(w, http.StatusOK, updated)
}

func (h *Handler) close(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireWrite(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// Validate the MR belongs to this repo BEFORE mutating state. (project_id,
	// number) is unique but a project can hold several repos, so close-by-
	// number alone could cross-close an MR that isn't ours to touch.
	current, err := h.MRs.GetMRByNumber(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if current.RepoID != rc.Repo.ID {
		httpapi.RenderError(w, r, apperr.NotFound("merge request"))
		return
	}
	mr, err := h.MRs.MarkClosed(r.Context(), rc.ProjectID, number, rc.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, mr)
}

func (h *Handler) reopen(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireWrite(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	current, err := h.MRs.GetMRByNumber(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if current.RepoID != rc.Repo.ID {
		httpapi.RenderError(w, r, apperr.NotFound("merge request"))
		return
	}
	mr, err := h.MRs.MarkReopened(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, mr)
}

// ----------------------------------------------------------------------------
// Comments
// ----------------------------------------------------------------------------

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireRead(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	comments, err := h.MRs.ListMRComments(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"comments": comments})
}

func (h *Handler) createComment(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireWrite(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createCommentReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	c, err := h.MRs.CreateMRComment(r.Context(), mrstore.CreateMRCommentParams{
		ProjectID: rc.ProjectID,
		Number:    number,
		AuthorID:  rc.UserID,
		Body:      req.Body,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, c)
}

// ----------------------------------------------------------------------------
// Reviews
// ----------------------------------------------------------------------------

func (h *Handler) listReviews(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireRead(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	reviews, err := h.MRs.ListMRReviews(r.Context(), rc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"reviews": reviews})
}

func (h *Handler) createReview(w http.ResponseWriter, r *http.Request) {
	rc, err := h.resolveRepo(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := requireWrite(rc); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createReviewReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	rev, err := h.MRs.CreateMRReview(r.Context(), mrstore.CreateMRReviewParams{
		ProjectID: rc.ProjectID,
		Number:    number,
		AuthorID:  rc.UserID,
		State:     req.State,
		Body:      req.Body,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, rev)
}

// ----------------------------------------------------------------------------
// Shared helpers
// ----------------------------------------------------------------------------

// resolveUserRef accepts either a UUID or a username and returns the user id.
func (h *Handler) resolveUserRef(r *http.Request, ref string) (uuid.UUID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return id, nil
	}
	u, err := h.Users.GetUserByUsername(r.Context(), ref)
	if err != nil {
		return uuid.Nil, err
	}
	return u.ID, nil
}

// resolveRefOrNotFound resolves a revspec to an OID, mapping libgit2's
// not-found case to apperr.NotFound(label) so the API returns a clean 404.
func resolveRefOrNotFound(repoPath, spec, label string) (string, error) {
	oid, err := git.Resolve(repoPath, spec)
	if err != nil {
		if git.IsNotFound(err) {
			return "", apperr.NotFound(label)
		}
		return "", apperr.Wrap(apperr.CodeInternal, "resolve "+label, err)
	}
	return oid, nil
}

// qualifyBranch ensures a branch name is fully qualified ("refs/heads/main").
// Bare names like "main" are commonly accepted in REST inputs; the libgit2
// merge-commit and FF update paths require fully qualified refs to avoid
// accidentally writing to a tag or remote-tracking ref.
func qualifyBranch(spec string) string {
	if strings.HasPrefix(spec, "refs/") {
		return spec
	}
	return "refs/heads/" + spec
}

// isTagRef reports whether spec is an explicit tag ref. Used by the create
// handler to reject MRs targeting tags before we resolve them.
func isTagRef(spec string) bool {
	return strings.HasPrefix(spec, "refs/tags/")
}

// wantPatch returns true if the include= query parameter contains "patch".
// We accept comma-separated tokens to leave room for future flags
// (?include=patch,stats etc.) without breaking the URL shape.
func wantPatch(v string) bool {
	if v == "" {
		return false
	}
	for _, tok := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(tok), "patch") {
			return true
		}
	}
	return false
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// lockTarget returns a function that releases a per-(repo, target_ref) lock
// the caller must defer. The map of locks is grown lazily; entries stay
// resident for the process lifetime, which is fine — the keyspace is bounded
// by (repo × branch) and the per-key footprint is one sync.Mutex.
func (h *Handler) lockTarget(repoID uuid.UUID, targetRef string) func() {
	key := repoID.String() + "\x00" + targetRef
	h.mergeMu.Lock()
	if h.mergeLocks == nil {
		h.mergeLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := h.mergeLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		h.mergeLocks[key] = mu
	}
	h.mergeMu.Unlock()
	mu.Lock()
	return mu.Unlock
}
