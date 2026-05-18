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

// Mount registers the admin subroutes. Mounted at /api/v1.
func (h *AdminHandler) Mount(r chi.Router) {
	r.Route("/admin", func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))
		r.Use(h.requireAdmin)
		r.Get("/users", h.listUsers)
		r.Patch("/users/{user_id}", h.patchUser)
	})
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

	// Refuse demote/deactivate operations that would leave zero active admins.
	// Counted server-side under the same request so a concurrent demote race
	// can't sneak past the check.
	target, err := h.Store.GetUserByID(r.Context(), userID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if target.IsAdmin && target.IsActive {
		demoting := req.IsAdmin != nil && !*req.IsAdmin
		deactivating := req.IsActive != nil && !*req.IsActive
		blocking := req.ApprovalStatus != nil && *req.ApprovalStatus != model.UserApprovalApproved
		if demoting || deactivating || blocking {
			n, err := h.Store.CountAdmins(r.Context())
			if err != nil {
				httpapi.RenderError(w, r, err)
				return
			}
			if n <= 1 {
				httpapi.RenderError(w, r, apperr.New(apperr.CodeConflict,
					"refusing to demote or disable the last active admin"))
				return
			}
		}
	}

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
