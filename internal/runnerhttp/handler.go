// Package runnerhttp exposes two surfaces:
//
//   - Management (JWT/OAT, maintainer+): mint runner registration tokens, list
//     and delete runners under an org.
//   - Runner protocol (wlrt_ token): register, heartbeat, acquire a job, stream
//     logs, report step status, complete a job, upload artifacts.
//
// Runners are ORG-SCOPED. The protocol auth middleware resolves the wlrt_
// bearer to a RunnerIdentity and pins every callback to that runner's id, so a
// runner can only touch jobs it was dispatched.
package runnerhttp

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/secretstore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires runner management + the runner protocol.
type Handler struct {
	Users     *userstore.Store
	Runners   *runnerstore.Store
	Pipelines *pipelinestore.Store
	Secrets   *secretstore.Store
	Verifier  *auth.Verifier
	OAT       auth.OATResolver

	// RegistrationTTL bounds how long a minted registration token is valid.
	RegistrationTTL time.Duration
	// CloneBaseURL is the origin the runner uses to git-clone (e.g.
	// "https://wuling.example.com"). Empty = derive from the request Host.
	CloneBaseURL string
	// DefaultTier is the org-config fallback tier (overridden per-org by
	// runner-config.yaml in the autoscaler path; used here only as a default
	// for the registration-token form).
	DefaultTier string
}

type runnerCtxKey struct{}

func runnerFromCtx(r *http.Request) *runnerstore.RunnerIdentity {
	ri, _ := r.Context().Value(runnerCtxKey{}).(*runnerstore.RunnerIdentity)
	return ri
}

// Mount registers both surfaces under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	// Management surface (user-authenticated).
	r.Group(func(r chi.Router) {
		r.Use(auth.MiddlewareBearer(auth.BearerResolver{JWT: h.Verifier, OAT: h.OAT}, false))
		r.Route("/orgs/{org_slug}/runners", func(r chi.Router) {
			r.Get("/", h.listRunners)
			r.Post("/registration-tokens", h.createRegistrationToken)
			r.Delete("/{runner_id}", h.deleteRunner)
		})
	})

	// Runner protocol surface. register authenticates via the body token; every
	// other endpoint requires a resolved wlrt_ bearer.
	r.Route("/runner", func(r chi.Router) {
		r.Post("/register", h.register)
		r.Group(func(r chi.Router) {
			r.Use(h.runnerAuth)
			r.Post("/heartbeat", h.heartbeat)
			r.Post("/jobs/acquire", h.acquire)
			r.Route("/jobs/{job_id}", func(r chi.Router) {
				r.Post("/logs", h.appendLog)
				r.Patch("/steps/{number}", h.patchStep)
				r.Post("/complete", h.complete)
				r.Post("/artifacts/{name}", h.uploadArtifact)
			})
		})
	})
}

// runnerAuth resolves the wlrt_ bearer and stashes the RunnerIdentity.
func (h *Handler) runnerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		tok := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		if tok == "" || tok == authz {
			httpapi.RenderError(w, r, apperr.Unauthorized("runner bearer token required"))
			return
		}
		ri, err := h.Runners.Resolve(r.Context(), tok)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), runnerCtxKey{}, ri)))
	})
}

// ----------------------------------------------------------------------------
// management surface
// ----------------------------------------------------------------------------

// orgManageCtx resolves the org and requires maintainer+.
func (h *Handler) orgManageCtx(r *http.Request) (orgID, userID uuid.UUID, role string, err error) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	org, err := h.Users.GetOrgBySlug(r.Context(), chi.URLParam(r, "org_slug"))
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	role, err = h.Users.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	if !auth.CanReadOrg(role) {
		return uuid.Nil, uuid.Nil, "", apperr.NotFound("org")
	}
	return org.ID, id.UserID, role, nil
}

func (h *Handler) listRunners(w http.ResponseWriter, r *http.Request) {
	orgID, _, _, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	runners, err := h.Runners.List(r.Context(), orgID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"runners": runners})
}

type createRegTokenReq struct {
	Labels       []string `json:"labels"        validate:"omitempty,dive,max=64"`
	ResourceTier string   `json:"resource_tier" validate:"omitempty,oneof=low medium high"`
}

func (h *Handler) createRegistrationToken(w http.ResponseWriter, r *http.Request) {
	orgID, userID, role, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !auth.CanManageMembers(role) {
		httpapi.RenderError(w, r, apperr.Forbidden("registering runners requires maintainer or above"))
		return
	}
	var req createRegTokenReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	tier := req.ResourceTier
	if tier == "" {
		tier = model.TierMedium
	}
	raw, err := h.Runners.CreateRegistrationToken(r.Context(), orgID, userID, runnerstore.RegistrationHints{
		Labels:       req.Labels,
		ResourceTier: tier,
		Provider:     "static",
	}, h.RegistrationTTL)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
		"token":      raw,
		"expires_in": int(h.RegistrationTTL.Seconds()),
	})
}

func (h *Handler) deleteRunner(w http.ResponseWriter, r *http.Request) {
	orgID, _, role, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !auth.CanManageMembers(role) {
		httpapi.RenderError(w, r, apperr.Forbidden("removing runners requires maintainer or above"))
		return
	}
	runnerID, perr := uuid.Parse(chi.URLParam(r, "runner_id"))
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid runner id"))
		return
	}
	if err := h.Runners.Delete(r.Context(), orgID, runnerID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
