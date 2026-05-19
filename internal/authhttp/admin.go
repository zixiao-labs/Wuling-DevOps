// Package authhttp — admin user-management endpoints.
//
// All routes mounted here require both a valid JWT and is_admin=true on the
// authenticated user. The admin gate is enforced server-side so a frontend
// bug that exposed the admin nav can't translate into actual privilege
// escalation.
package authhttp

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// AdminHandler bundles dependencies for /api/v1/admin/* routes.
type AdminHandler struct {
	Store    *userstore.Store
	Verifier *auth.Verifier
}

// Mount registers the admin subroutes under /admin and applies the admin
// guard middlewares itself. Kept for callers that wire authhttp standalone.
func (h *AdminHandler) Mount(r chi.Router) {
	r.Route("/admin", func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))
		r.Use(h.requireAdmin)
		h.MountInner(r)
	})
}

// MountInner registers the admin user-management endpoints onto a router
// that already has JWT + admin-guard middlewares applied. Used by the main
// server.go where /admin is a shared route with other admin handlers
// (oauthhttp), so we mount the guards once at the route level.
func (h *AdminHandler) MountInner(r chi.Router) {
	r.Get("/users", h.listUsers)
	r.Patch("/users/{user_id}", h.patchUser)
}

// requireAdmin is a middleware that loads the authenticated user from the DB
// and refuses the request unless they're an active admin. We load fresh on
// every request rather than trusting a claim in the JWT, so demoting an
// admin takes effect immediately rather than after token rotation.
func (h *AdminHandler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := auth.RequireIdentity(r)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		u, err := h.Store.GetUserByID(r.Context(), id.UserID)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		if !u.IsAdmin || !u.IsActive || u.ApprovalStatus != model.UserApprovalApproved {
			httpapi.RenderError(w, r, apperr.Forbidden("admin role required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *AdminHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.ListUsers(r.Context(), userstore.ListUsersParams{
		Status: r.URL.Query().Get("status"),
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if users == nil {
		users = []model.User{}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"users": users})
}

type patchUserReq struct {
	ApprovalStatus *string `json:"approval_status" validate:"omitempty,oneof=pending approved rejected"`
	ApprovalNote   *string `json:"approval_note"   validate:"omitempty,max=512"`
	IsAdmin        *bool   `json:"is_admin"`
	IsActive       *bool   `json:"is_active"`
}

func (h *AdminHandler) patchUser(w http.ResponseWriter, r *http.Request) {
	caller, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	userIDStr := chi.URLParam(r, "user_id")
	userID, perr := uuid.Parse(userIDStr)
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid user id"))
		return
	}

	var req patchUserReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	// The "refuse to demote the last active admin" guard lives inside
	// Store.UpdateUser so it can run with row-level locks under the same
	// transaction as the write — checking it here would be a TOCTOU race.
	approvedBy := caller.UserID
	updated, err := h.Store.UpdateUser(r.Context(), userID, userstore.UpdateUserParams{
		ApprovalStatus: req.ApprovalStatus,
		ApprovalNote:   req.ApprovalNote,
		IsAdmin:        req.IsAdmin,
		IsActive:       req.IsActive,
		ApprovedBy:     &approvedBy,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, updated)
}
