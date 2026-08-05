package runnerhttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/runnercheck"
)

// AdminOrgLookup is the minimal organization lookup required by the global
// admin runner self-check surface.
type AdminOrgLookup interface {
	GetOrgBySlug(ctx context.Context, slug string) (*model.Org, error)
}

// AdminRunnerSelfCheckHandler starts and lists durable, billable Runner
// self-checks. MountInner must be attached to the shared /api/v1/admin router,
// which applies the existing fresh active-is_admin check before this handler.
type AdminRunnerSelfCheckHandler struct {
	Orgs   AdminOrgLookup
	Checks *runnercheck.Service
}

// MountInner mounts routes on a router already protected by the server's
// JWT + requireAdmin middleware. It intentionally does not add org membership
// authorization: active global administrators can inspect any organization.
func (h *AdminRunnerSelfCheckHandler) MountInner(r chi.Router) {
	r.Get("/runner-self-checks", h.listRunnerSelfChecks)
	r.Post("/runner-self-checks", h.createRunnerSelfCheck)
}

type createRunnerSelfCheckReq struct {
	OrgSlug   string   `json:"org_slug"   validate:"required,min=2,max=64"`
	PoolNames []string `json:"pool_names" validate:"omitempty,max=64,dive,max=128"`
}

func (h *AdminRunnerSelfCheckHandler) listRunnerSelfChecks(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.RequireIdentity(r); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	org, err := h.orgFromSlug(r.Context(), r.URL.Query().Get("org_slug"))
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if h.Checks == nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "runner self-check service is unavailable"))
		return
	}
	checks, err := h.Checks.ListAudits(r.Context(), org.ID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"checks": checks})
}

func (h *AdminRunnerSelfCheckHandler) createRunnerSelfCheck(w http.ResponseWriter, r *http.Request) {
	caller, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createRunnerSelfCheckReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	org, err := h.orgFromSlug(r.Context(), req.OrgSlug)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if h.Checks == nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "runner self-check service is unavailable"))
		return
	}

	result, err := h.Checks.Start(r.Context(), runnercheck.Request{
		OrgID:       org.ID,
		OrgSlug:     org.Slug,
		RequestedBy: caller.UserID,
		PoolNames:   req.PoolNames,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (h *AdminRunnerSelfCheckHandler) orgFromSlug(ctx context.Context, raw string) (*model.Org, error) {
	slug := strings.TrimSpace(raw)
	if slug == "" {
		return nil, apperr.New(apperr.CodeBadRequest, "org_slug is required")
	}
	if len(slug) > 64 {
		return nil, apperr.New(apperr.CodeBadRequest, "org_slug is too long")
	}
	if h == nil || h.Orgs == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "organization lookup is unavailable")
	}
	org, err := h.Orgs.GetOrgBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if org == nil || org.ID == uuid.Nil {
		return nil, apperr.NotFound("org")
	}
	return org, nil
}
