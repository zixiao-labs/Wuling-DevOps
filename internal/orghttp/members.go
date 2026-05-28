// Package orghttp — member management endpoints.
//
//	GET    /api/v1/orgs/{slug}/members           list members
//	PATCH  /api/v1/orgs/{slug}/members/{user_id} change role
//	DELETE /api/v1/orgs/{slug}/members/{user_id} remove (also self-leave)
//
// Permissions:
//
//   - Any member may GET the member list (it's part of the org metadata they
//     already have access to).
//   - PATCH and DELETE require auth.CanManageMembers, plus an additional
//     CanAssignRole check on the target role for PATCH so a maintainer can't
//     promote anybody to owner.
//   - Self-leave (DELETE on your own user_id) is always allowed for any
//     member regardless of role — the last-owner guard in the store still
//     applies, so the last owner can't accidentally lock themselves out.

package orghttp

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
)

func (h *Handler) mountMembers(r chi.Router) {
	r.Route("/members", func(r chi.Router) {
		r.Get("/", h.listMembers)
		r.Patch("/{user_id}", h.patchMember)
		r.Delete("/{user_id}", h.removeMember)
	})
}

type patchMemberReq struct {
	Role string `json:"role" validate:"required,oneof=owner maintainer developer reporter guest"`
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
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
	members, err := h.Store.ListMembers(r.Context(), org.ID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"members": members,
		"role":    role, // echo caller's own role so the UI can gate buttons
	})
}

func (h *Handler) patchMember(w http.ResponseWriter, r *http.Request) {
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
	actorRole, err := h.Store.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if actorRole == "" {
		httpapi.RenderError(w, r, apperr.NotFound("org"))
		return
	}
	if !auth.CanManageMembers(actorRole) {
		httpapi.RenderError(w, r, apperr.Forbidden("only org owners and maintainers can manage members"))
		return
	}

	targetID, perr := uuid.Parse(chi.URLParam(r, "user_id"))
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid user id"))
		return
	}
	var req patchMemberReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	if !auth.CanAssignRole(actorRole, req.Role) {
		httpapi.RenderError(w, r, apperr.Forbidden(
			"your role does not permit granting "+req.Role))
		return
	}

	// Forbid changing your own role here — owners who want to step down must
	// promote someone else first, then leave or get demoted by the new owner.
	// Without this, an owner could accidentally bypass the last-owner guard
	// by demoting themselves to maintainer.
	if targetID == id.UserID {
		httpapi.RenderError(w, r, apperr.Conflict("cannot change your own role; ask another owner"))
		return
	}

	// Outrank check: a maintainer must not be able to change another
	// maintainer's role even though CanAssignRole permits granting the new
	// role. Mirrors the DELETE handler's outrank guard.
	targetRole, err := h.Store.MemberRole(r.Context(), org.ID, targetID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if targetRole == "" {
		httpapi.RenderError(w, r, apperr.NotFound("member"))
		return
	}
	if auth.RoleLevel(actorRole) <= auth.RoleLevel(targetRole) {
		httpapi.RenderError(w, r, apperr.Forbidden(
			"your role does not outrank the target member"))
		return
	}

	if err := h.Store.SetMemberRole(r.Context(), org.ID, targetID, req.Role); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
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
	actorRole, err := h.Store.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if actorRole == "" {
		httpapi.RenderError(w, r, apperr.NotFound("org"))
		return
	}

	targetID, perr := uuid.Parse(chi.URLParam(r, "user_id"))
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid user id"))
		return
	}

	// Self-leave: any member can leave an org they're part of (subject to the
	// last-owner guard, which the store enforces). Removing somebody else
	// requires manage-members permission AND a higher rank than the target,
	// so an owner can't be evicted by a maintainer.
	if targetID != id.UserID {
		if !auth.CanManageMembers(actorRole) {
			httpapi.RenderError(w, r, apperr.Forbidden("only org owners and maintainers can remove members"))
			return
		}
		targetRole, err := h.Store.MemberRole(r.Context(), org.ID, targetID)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		if targetRole == "" {
			httpapi.RenderError(w, r, apperr.NotFound("member"))
			return
		}
		if auth.RoleLevel(actorRole) <= auth.RoleLevel(targetRole) {
			httpapi.RenderError(w, r, apperr.Forbidden(
				"your role does not outrank the target member"))
			return
		}
	}

	if err := h.Store.RemoveMember(r.Context(), org.ID, targetID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
