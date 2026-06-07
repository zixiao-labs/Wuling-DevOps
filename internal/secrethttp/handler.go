// Package secrethttp exposes org- and project-scoped Secrets management.
// Managing secrets (set/delete/list) requires maintainer-or-above — the same
// tier that manages members and CI config. Values are write-only over the
// API: a PUT accepts a value, but no endpoint ever returns one (only names +
// metadata). Plaintext leaves the system solely to a runner executing a job.
package secrethttp

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/secretstore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires Secrets endpoints.
type Handler struct {
	Users    *userstore.Store
	Secrets  *secretstore.Store
	Verifier *auth.Verifier
	OAT      auth.OATResolver
}

// Mount registers routes under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.MiddlewareBearer(auth.BearerResolver{JWT: h.Verifier, OAT: h.OAT}, false))

		r.Route("/orgs/{org_slug}/secrets", func(r chi.Router) {
			r.Get("/", h.listOrgSecrets)
			r.Put("/{name}", h.putOrgSecret)
			r.Delete("/{name}", h.deleteOrgSecret)
		})
		r.Route("/orgs/{org_slug}/projects/{project_slug}/secrets", func(r chi.Router) {
			r.Get("/", h.listProjectSecrets)
			r.Put("/{name}", h.putProjectSecret)
			r.Delete("/{name}", h.deleteProjectSecret)
		})
	})
}

type setSecretReq struct {
	Value string `json:"value" validate:"required,max=65536"`
}

// orgManageCtx resolves the org and verifies the caller is a maintainer+.
func (h *Handler) orgManageCtx(r *http.Request) (orgID uuid.UUID, userID uuid.UUID, err error) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	org, err := h.Users.GetOrgBySlug(r.Context(), chi.URLParam(r, "org_slug"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	role, err := h.Users.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if !auth.CanReadOrg(role) {
		return uuid.Nil, uuid.Nil, apperr.NotFound("org")
	}
	if !auth.CanManageMembers(role) {
		return uuid.Nil, uuid.Nil, apperr.Forbidden("managing secrets requires maintainer or above")
	}
	return org.ID, id.UserID, nil
}

func (h *Handler) listOrgSecrets(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	secs, err := h.Secrets.ListOrg(r.Context(), orgID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"secrets": secs})
}

func (h *Handler) putOrgSecret(w http.ResponseWriter, r *http.Request) {
	orgID, userID, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req setSecretReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	sec, err := h.Secrets.Set(r.Context(), secretstore.SetParams{
		OrgID: orgID, Name: chi.URLParam(r, "name"), Value: req.Value, CreatedBy: userID,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, sec)
}

func (h *Handler) deleteOrgSecret(w http.ResponseWriter, r *http.Request) {
	orgID, _, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := h.Secrets.DeleteOrg(r.Context(), orgID, chi.URLParam(r, "name")); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// projectManageCtx resolves org+project and verifies maintainer+.
func (h *Handler) projectManageCtx(r *http.Request) (orgID, projectID, userID uuid.UUID, err error) {
	orgID, userID, err = h.orgManageCtx(r)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	project, err := h.Users.GetProjectBySlug(r.Context(), orgID, chi.URLParam(r, "project_slug"))
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	return orgID, project.ID, userID, nil
}

func (h *Handler) listProjectSecrets(w http.ResponseWriter, r *http.Request) {
	_, projectID, _, err := h.projectManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	secs, err := h.Secrets.ListProject(r.Context(), projectID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"secrets": secs})
}

func (h *Handler) putProjectSecret(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, userID, err := h.projectManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req setSecretReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	pid := projectID
	sec, err := h.Secrets.Set(r.Context(), secretstore.SetParams{
		OrgID: orgID, ProjectID: &pid, Name: chi.URLParam(r, "name"), Value: req.Value, CreatedBy: userID,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, sec)
}

func (h *Handler) deleteProjectSecret(w http.ResponseWriter, r *http.Request) {
	_, projectID, _, err := h.projectManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := h.Secrets.DeleteProject(r.Context(), projectID, chi.URLParam(r, "name")); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
