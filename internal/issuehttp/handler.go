// Package issuehttp wires HTTP handlers for the Issues domain: issues,
// labels, comments, and assignees.
//
// Routes are mounted under
// "/api/v1/orgs/{org_slug}/projects/{project_slug}/issues" so they nest
// under the same org / project hierarchy as repos. Authorization is
// uniform:
//
//   - Any org member can read.
//   - Any org member can create.
//   - The author or an org owner/admin can edit (PATCH).
//   - Only an org owner/admin can delete an issue or a comment.
//
// Slug lookups defer to userstore. Bodies are validated against the
// canonical apperr envelope.
package issuehttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/issuestore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires issue handlers.
type Handler struct {
	Users    *userstore.Store
	Issues   *issuestore.Store
	Verifier *auth.Verifier
}

// Mount registers routes under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	r.Route("/orgs/{org_slug}/projects/{project_slug}", func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))

		r.Route("/issues", func(r chi.Router) {
			r.Get("/", h.listIssues)
			r.Post("/", h.createIssue)
			r.Route("/{number}", func(r chi.Router) {
				r.Get("/", h.getIssue)
				r.Patch("/", h.patchIssue)
				r.Delete("/", h.deleteIssue)
				r.Get("/comments", h.listComments)
				r.Post("/comments", h.createComment)
			})
		})

		r.Route("/labels", func(r chi.Router) {
			r.Get("/", h.listLabels)
			r.Post("/", h.createLabel)
			r.Delete("/{label_id}", h.deleteLabel)
		})
	})
}

// ----------------------------------------------------------------------------
// authorization helpers
// ----------------------------------------------------------------------------

// projectCtx holds the resolved org/project plus the caller's role in that
// org. Returned by resolveProject so handlers don't repeat themselves.
type projectCtx struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	UserID    uuid.UUID
	Username  string
	Role      string // "owner", "admin", "member", or "" (not a member)
}

// resolveProject loads the org+project from URL slugs and resolves the
// caller's membership role. Returns 404 (not 403) when the caller is not
// a member of the org so we don't leak which orgs exist.
func (h *Handler) resolveProject(r *http.Request) (*projectCtx, error) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		return nil, err
	}
	org, err := h.Users.GetOrgBySlug(r.Context(), chi.URLParam(r, "org_slug"))
	if err != nil {
		return nil, err
	}
	role, err := h.Users.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		return nil, err
	}
	if role == "" {
		return nil, apperr.NotFound("project")
	}
	project, err := h.Users.GetProjectBySlug(r.Context(), org.ID, chi.URLParam(r, "project_slug"))
	if err != nil {
		return nil, err
	}
	return &projectCtx{
		OrgID:     org.ID,
		ProjectID: project.ID,
		UserID:    id.UserID,
		Username:  id.Username,
		Role:      role,
	}, nil
}

// canAdmin reports whether role is "owner" or "admin".
func canAdmin(role string) bool { return role == "owner" || role == "admin" }

// parseNumber pulls the {number} URL parameter as a positive int64.
func parseNumber(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "number")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 0, apperr.New(apperr.CodeBadRequest, "invalid issue number")
	}
	return n, nil
}

// ----------------------------------------------------------------------------
// issue handlers
// ----------------------------------------------------------------------------

type createIssueReq struct {
	Title     string      `json:"title"      validate:"required,min=1,max=256"`
	Body      string      `json:"body"       validate:"max=65536"`
	Labels    []uuid.UUID `json:"labels"     validate:"omitempty,dive,uuid4"`
	Assignees []uuid.UUID `json:"assignees"  validate:"omitempty,dive,uuid4"`
}

type patchIssueReq struct {
	Title     *string      `json:"title,omitempty"     validate:"omitempty,min=1,max=256"`
	Body      *string      `json:"body,omitempty"      validate:"omitempty,max=65536"`
	State     *string      `json:"state,omitempty"     validate:"omitempty,oneof=open closed"`
	Labels    *[]uuid.UUID `json:"labels,omitempty"    validate:"omitempty,dive,uuid4"`
	Assignees *[]uuid.UUID `json:"assignees,omitempty" validate:"omitempty,dive,uuid4"`
}

type createCommentReq struct {
	Body string `json:"body" validate:"required,min=1,max=65536"`
}

func (h *Handler) listIssues(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	q := r.URL.Query()
	f := issuestore.ListIssuesFilter{
		State:     strings.ToLower(strings.TrimSpace(q.Get("state"))),
		LabelName: q.Get("label"),
		Search:    q.Get("search"),
	}
	if f.State != "" && f.State != "open" && f.State != "closed" {
		httpapi.RenderError(w, r,
			apperr.New(apperr.CodeBadRequest, "state must be 'open' or 'closed'"))
		return
	}
	if a := strings.TrimSpace(q.Get("assignee")); a != "" {
		uid, err := h.resolveUserRef(r, a)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		f.AssigneeID = uid
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
			httpapi.RenderError(w, r,
				apperr.New(apperr.CodeBadRequest, "invalid limit parameter"))
			return
		}
		f.Limit = n
	}
	issues, err := h.Issues.ListIssues(r.Context(), pc.ProjectID, f)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"issues": issues})
}

func (h *Handler) createIssue(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createIssueReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	iss, err := h.Issues.CreateIssue(r.Context(), issuestore.CreateIssueParams{
		ProjectID:   pc.ProjectID,
		AuthorID:    pc.UserID,
		Title:       req.Title,
		Body:        req.Body,
		LabelIDs:    req.Labels,
		AssigneeIDs: req.Assignees,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, iss)
}

func (h *Handler) getIssue(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	iss, err := h.Issues.GetIssueByNumber(r.Context(), pc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, iss)
}

func (h *Handler) patchIssue(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// Load the current issue to authorize: author or org admin can edit.
	current, err := h.Issues.GetIssueByNumber(r.Context(), pc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !canAdmin(pc.Role) && (current.Author == nil || current.Author.ID != pc.UserID) {
		httpapi.RenderError(w, r,
			apperr.Forbidden("only the author or an org owner/admin can edit this issue"))
		return
	}

	var req patchIssueReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	iss, err := h.Issues.UpdateIssue(r.Context(), pc.ProjectID, number, issuestore.UpdateIssueParams{
		Title:       req.Title,
		Body:        req.Body,
		State:       req.State,
		LabelIDs:    req.Labels,
		AssigneeIDs: req.Assignees,
		ActorID:     pc.UserID,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, iss)
}

func (h *Handler) deleteIssue(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !canAdmin(pc.Role) {
		httpapi.RenderError(w, r,
			apperr.Forbidden("only org owners/admins can delete issues"))
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := h.Issues.DeleteIssue(r.Context(), pc.ProjectID, number); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// comment handlers
// ----------------------------------------------------------------------------

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, err := parseNumber(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	comments, err := h.Issues.ListComments(r.Context(), pc.ProjectID, number)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"comments": comments})
}

func (h *Handler) createComment(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
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
	c, err := h.Issues.CreateComment(r.Context(), issuestore.CreateCommentParams{
		ProjectID: pc.ProjectID,
		Number:    number,
		AuthorID:  pc.UserID,
		Body:      req.Body,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, c)
}

// ----------------------------------------------------------------------------
// label handlers
// ----------------------------------------------------------------------------

type createLabelReq struct {
	Name        string `json:"name"        validate:"required,min=1,max=64"`
	Color       string `json:"color"       validate:"omitempty,hexadecimal"`
	Description string `json:"description" validate:"max=256"`
}

func (h *Handler) listLabels(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	labels, err := h.Issues.ListLabels(r.Context(), pc.ProjectID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"labels": labels})
}

func (h *Handler) createLabel(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !canAdmin(pc.Role) {
		httpapi.RenderError(w, r,
			apperr.Forbidden("only org owners/admins can manage labels"))
		return
	}
	var req createLabelReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// Normalize color before validation
	color := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Color), "#"))
	if color != "" && len(color) != 6 {
		httpapi.RenderError(w, r, apperr.Validation("color must be exactly 6 hexadecimal characters", nil))
		return
	}
	l, err := h.Issues.CreateLabel(r.Context(), issuestore.CreateLabelParams{
		ProjectID:   pc.ProjectID,
		Name:        req.Name,
		Color:       color,
		Description: req.Description,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, l)
}

func (h *Handler) deleteLabel(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !canAdmin(pc.Role) {
		httpapi.RenderError(w, r,
			apperr.Forbidden("only org owners/admins can manage labels"))
		return
	}
	idStr := chi.URLParam(r, "label_id")
	labelID, perr := uuid.Parse(idStr)
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid label id"))
		return
	}
	if err := h.Issues.DeleteLabel(r.Context(), pc.ProjectID, labelID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// shared helpers
// ----------------------------------------------------------------------------

// resolveUserRef accepts either a UUID or a username and returns the
// underlying user id. Used by listIssues so callers can write
// "?author=alice" or "?assignee=<uuid>" interchangeably.
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
