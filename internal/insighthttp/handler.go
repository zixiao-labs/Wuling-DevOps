// Package insighthttp wires HTTP handlers for the Insights domain.
//
// Three endpoints mount under
//   /api/v1/orgs/{org_slug}/projects/{project_slug}/insights
//
//	GET /activity?since=30d                                 — per-day activity
//	GET /contributors?repo=<slug>&since=30d&limit=20        — top committers
//	GET /languages?repo=<slug>&ref=<branch>                 — bytes per lang
//
// Authorization mirrors issuehttp / mrhttp: any org member can read; we don't
// gate Insights by role beyond membership.
package insighthttp

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/insightstore"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires insights routes.
type Handler struct {
	Users    *userstore.Store
	Insights *insightstore.Store
	Layout   *repostore.Layout
	Verifier *auth.Verifier
	// OAT resolves OAuth-provider access tokens (wloat_…) so third-party
	// OAuth clients can read insights with a bearer. When nil, OAT-shaped
	// bearers are rejected with the standard 401.
	OAT auth.OATResolver
}

// Mount registers routes under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	r.Route("/orgs/{org_slug}/projects/{project_slug}/insights", func(r chi.Router) {
		r.Use(auth.MiddlewareBearer(auth.BearerResolver{JWT: h.Verifier, OAT: h.OAT}, false))
		r.Get("/activity", h.activity)
		r.Get("/contributors", h.contributors)
		r.Get("/languages", h.languages)
	})
}

// ----------------------------------------------------------------------------
// authorization helpers — single-source copy of the issuehttp pattern. Kept
// inline so this package doesn't reach into issuehttp internals.
// ----------------------------------------------------------------------------

type projectCtx struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	UserID    uuid.UUID
}

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
	return &projectCtx{OrgID: org.ID, ProjectID: project.ID, UserID: id.UserID}, nil
}

// ----------------------------------------------------------------------------
// handlers
// ----------------------------------------------------------------------------

func (h *Handler) activity(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	since, err := insightstore.ParseSince(r.URL.Query().Get("since"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	days, err := h.Insights.Activity(r.Context(), pc.ProjectID, since)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"days": days})
}

func (h *Handler) contributors(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	repoSlug := r.URL.Query().Get("repo")
	if repoSlug == "" {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "?repo=... is required"))
		return
	}
	repo, err := h.Users.GetRepoBySlug(r.Context(), pc.ProjectID, repoSlug)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	since, err := insightstore.ParseSince(r.URL.Query().Get("since"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, perr := strconv.Atoi(l); perr == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	stats, err := h.Insights.Contributors(r.Context(), repo.ID, since, limit)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"contributors": stats})
}

func (h *Handler) languages(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	repoSlug := r.URL.Query().Get("repo")
	if repoSlug == "" {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "?repo=... is required"))
		return
	}
	repo, err := h.Users.GetRepoBySlug(r.Context(), pc.ProjectID, repoSlug)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if repo.IsEmpty {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{
			"bytes": map[string]int64{},
			"files": map[string]int64{},
		})
		return
	}
	repoPath := h.Layout.Path(pc.OrgID, pc.ProjectID, repo.ID)
	stats, err := h.Insights.Languages(repoPath, r.URL.Query().Get("ref"), repo.DefaultBranch)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, stats)
}
