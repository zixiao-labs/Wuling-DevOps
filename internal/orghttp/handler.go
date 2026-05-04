// Package orghttp wires HTTP handlers for orgs and the projects nested under
// them. Authorization is uniform: a user must be a member of the org to read,
// and an owner/admin to write. All read-paths still require auth in Stage 1
// (no anonymous reads yet — visibility=public is metadata-only).
package orghttp

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires org/project handlers.
type Handler struct {
	Store    *userstore.Store
	Verifier *auth.Verifier
}

// Mount registers routes. We wrap in Group() so r.Use() doesn't conflict
// with sibling handlers that have already attached routes to the parent
// router (chi forbids middleware-after-routes on the same mux).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))

		r.Route("/orgs", func(r chi.Router) {
			r.Get("/", h.listOrgs)
			r.Post("/", h.createOrg)
			r.Route("/{org_slug}", func(r chi.Router) {
				r.Get("/", h.getOrg)
				r.Route("/projects", func(r chi.Router) {
					r.Get("/", h.listProjects)
					r.Post("/", h.createProject)
					r.Get("/{project_slug}", h.getProject)
				})
			})
		})
	})
}

// ----------------------------------------------------------------------------
// orgs
// ----------------------------------------------------------------------------

type createOrgReq struct {
	Slug        string `json:"slug"         validate:"required,min=2,max=64,alphanumdash"`
	DisplayName string `json:"display_name" validate:"max=128"`
	Description string `json:"description"  validate:"max=512"`
}

func (h *Handler) listOrgs(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	orgs, err := h.Store.ListOrgsForUser(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"orgs": orgs})
}

func (h *Handler) createOrg(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createOrgReq
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	org, err := h.Store.CreateOrg(r.Context(), userstore.CreateOrgParams{
		Slug:        strings.TrimSpace(req.Slug),
		DisplayName: req.DisplayName,
		Description: req.Description,
		OwnerUserID: id.UserID,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, org)
}

func (h *Handler) getOrg(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	slug := chi.URLParam(r, "org_slug")
	org, err := h.Store.GetOrgBySlug(r.Context(), slug)
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
	httpapi.WriteJSON(w, http.StatusOK, org)
}

// ----------------------------------------------------------------------------
// projects
// ----------------------------------------------------------------------------

type createProjectReq struct {
	Slug        string `json:"slug"         validate:"required,min=2,max=64,alphanumdash"`
	DisplayName string `json:"display_name" validate:"max=128"`
	Description string `json:"description"  validate:"max=512"`
	Visibility  string `json:"visibility"   validate:"omitempty,oneof=private internal public"`
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
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
	projects, err := h.Store.ListProjects(r.Context(), org.ID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
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
	if role != "owner" && role != "admin" {
		httpapi.RenderError(w, r, apperr.Forbidden("only org owners/admins can create projects"))
		return
	}
	var req createProjectReq
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	project, err := h.Store.CreateProject(r.Context(), userstore.CreateProjectParams{
		OrgID:       org.ID,
		Slug:        strings.TrimSpace(req.Slug),
		DisplayName: req.DisplayName,
		Description: req.Description,
		Visibility:  req.Visibility,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, project)
}

func (h *Handler) getProject(w http.ResponseWriter, r *http.Request) {
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
	httpapi.WriteJSON(w, http.StatusOK, project)
}
