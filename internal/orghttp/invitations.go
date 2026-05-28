// Package orghttp — invitation endpoints (magic-link flow).
//
//	POST   /api/v1/orgs/{slug}/invitations          create invite, returns token+url
//	GET    /api/v1/orgs/{slug}/invitations          list invites (admin view)
//	DELETE /api/v1/orgs/{slug}/invitations/{id}     revoke pending invite
//	GET    /api/v1/invitations/{token}              preview by token (recipient view)
//	POST   /api/v1/invitations/{token}/accept       accept the invitation
//
// The invitee can be specified by username OR email. If the username resolves
// to an existing user, we store invitee_user_id and the recipient's identity
// is matched against that id; otherwise we store the lowercased email and the
// recipient must be signed in with a matching email when they accept.

package orghttp

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

func (h *Handler) mountInvitations(r chi.Router) {
	r.Route("/invitations", func(r chi.Router) {
		r.Get("/", h.listInvitations)
		r.Post("/", h.createInvitation)
		r.Delete("/{invitation_id}", h.revokeInvitation)
	})
}

type createInvitationReq struct {
	// Identifier is either a username or an email. Required.
	Identifier string `json:"identifier" validate:"required,min=1,max=320"`
	// Role to grant on accept. Owner is intentionally not in the enum —
	// owner promotion goes through PATCH /members/{id}, not via link.
	Role string `json:"role" validate:"required,oneof=maintainer developer reporter guest"`
	// TTLHours bounds how long the invitation stays acceptable. Defaults to
	// 7 days when zero or negative; capped at 30 days.
	TTLHours int `json:"ttl_hours" validate:"omitempty,min=1,max=720"`
}

type createInvitationResp struct {
	Invitation *invitationResp `json:"invitation"`
	URL        string          `json:"url"`
}

// invitationResp mirrors model.OrgInvitation but is the shape returned by
// orghttp specifically — kept separate so we can elide fields the admin list
// doesn't need (e.g. raw token after creation).
type invitationResp struct {
	ID            uuid.UUID  `json:"id"`
	OrgID         uuid.UUID  `json:"org_id"`
	OrgSlug       string     `json:"org_slug,omitempty"`
	OrgDisplay    string     `json:"org_display_name,omitempty"`
	InviteeUserID *uuid.UUID `json:"invitee_user_id,omitempty"`
	InviteeEmail  string     `json:"invitee_email,omitempty"`
	Role          string     `json:"role"`
	Status        string     `json:"status"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	Token         string     `json:"token,omitempty"`
	URL           string     `json:"url,omitempty"`
	Inviter       *userRef   `json:"inviter,omitempty"`
}

type userRef struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
}

func (h *Handler) createInvitation(w http.ResponseWriter, r *http.Request) {
	if h.Hasher == nil {
		httpapi.RenderError(w, r, apperr.Internal(nil))
		return
	}
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
		httpapi.RenderError(w, r, apperr.Forbidden("only org owners and maintainers can invite members"))
		return
	}

	var req createInvitationReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// Authoriser must outrank the grant: maintainer can't invite at maintainer.
	if !auth.CanAssignRole(actorRole, req.Role) {
		httpapi.RenderError(w, r, apperr.Forbidden(
			"your role does not permit granting "+req.Role))
		return
	}

	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		httpapi.RenderError(w, r, apperr.Validation("identifier required", nil))
		return
	}
	params := userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: id.UserID,
		Role:          req.Role,
	}
	if strings.Contains(identifier, "@") {
		params.InviteeEmail = identifier
		// Best-effort: if there is already a user with this email, attach the
		// invite to their user_id so the accept-time match is tighter (we
		// compare on id, not on a possibly-updated email).
		if u, err := h.Store.GetUserByEmail(r.Context(), identifier); err == nil {
			params.InviteeUserID = &u.ID
			params.InviteeEmail = "" // collapse to user-id path
		}
	} else {
		u, err := h.Store.GetUserByUsername(r.Context(), identifier)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		params.InviteeUserID = &u.ID
	}

	// Refuse to invite someone who is already a member at any role — the
	// admin should use PATCH /members/{id} to change their role instead. We
	// only check the user-id path; the email-only path can't (yet) resolve.
	if params.InviteeUserID != nil {
		existing, err := h.Store.MemberRole(r.Context(), org.ID, *params.InviteeUserID)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		if existing != "" {
			httpapi.RenderError(w, r, apperr.Conflict("user is already a member of this org"))
			return
		}
	}

	if req.TTLHours > 0 {
		params.TTL = time.Duration(req.TTLHours) * time.Hour
	}

	raw, hashed, terr := auth.NewInvitationToken(h.Hasher)
	if terr != nil {
		httpapi.RenderError(w, r, apperr.Internal(terr))
		return
	}
	params.TokenHash = hashed

	inv, err := h.Store.CreateInvitation(r.Context(), params)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	inv.OrgSlug = org.Slug
	inv.OrgDisplayName = org.DisplayName
	inv.Token = raw

	url := h.buildInviteURL(raw)
	resp := &invitationResp{
		ID:            inv.ID,
		OrgID:         inv.OrgID,
		OrgSlug:       inv.OrgSlug,
		OrgDisplay:    inv.OrgDisplayName,
		InviteeUserID: inv.InviteeUserID,
		InviteeEmail:  inv.InviteeEmail,
		Role:          inv.Role,
		Status:        inv.Status,
		ExpiresAt:     inv.ExpiresAt,
		CreatedAt:     inv.CreatedAt,
		AcceptedAt:    inv.AcceptedAt,
		Token:         raw,
		URL:           url,
	}
	httpapi.WriteJSON(w, http.StatusCreated, createInvitationResp{
		Invitation: resp,
		URL:        url,
	})
}

func (h *Handler) listInvitations(w http.ResponseWriter, r *http.Request) {
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
		httpapi.RenderError(w, r, apperr.Forbidden("only org owners and maintainers can list invitations"))
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	invs, err := h.Store.ListInvitations(r.Context(), org.ID, status)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	out := make([]invitationResp, 0, len(invs))
	for _, inv := range invs {
		item := invitationResp{
			ID:            inv.ID,
			OrgID:         inv.OrgID,
			OrgSlug:       org.Slug,
			OrgDisplay:    org.DisplayName,
			InviteeUserID: inv.InviteeUserID,
			InviteeEmail:  inv.InviteeEmail,
			Role:          inv.Role,
			Status:        inv.Status,
			ExpiresAt:     inv.ExpiresAt,
			CreatedAt:     inv.CreatedAt,
			AcceptedAt:    inv.AcceptedAt,
		}
		if inv.Inviter != nil {
			item.Inviter = &userRef{
				ID:          inv.Inviter.ID,
				Username:    inv.Inviter.Username,
				DisplayName: inv.Inviter.DisplayName,
			}
		}
		out = append(out, item)
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"invitations": out})
}

func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request) {
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
		httpapi.RenderError(w, r, apperr.Forbidden("only org owners and maintainers can revoke invitations"))
		return
	}
	invID, perr := uuid.Parse(chi.URLParam(r, "invitation_id"))
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid invitation id"))
		return
	}
	if err := h.Store.RevokeInvitation(r.Context(), org.ID, invID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getInvitationByToken is the recipient-facing preview endpoint. It returns
// just enough info to render an "accept invitation" page: org name, role,
// expiry, status. We DO NOT enforce that the caller matches the invitee here
// — the recipient should be able to see why a link doesn't work for them
// (e.g. "this invitation is for foo@example.com, but you're logged in as
// bar@example.com"). Acceptance does enforce the match.
func (h *Handler) getInvitationByToken(w http.ResponseWriter, r *http.Request) {
	if h.Hasher == nil {
		httpapi.RenderError(w, r, apperr.Internal(nil))
		return
	}
	raw := strings.TrimSpace(chi.URLParam(r, "token"))
	if raw == "" {
		httpapi.RenderError(w, r, apperr.NotFound("invitation"))
		return
	}
	hash := h.Hasher.Hash(raw)
	inv, err := h.Store.GetInvitationByTokenHash(r.Context(), hash)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// Auto-flag obviously stale invitations so the UI can render the right
	// state without comparing timestamps client-side.
	status := inv.Status
	if status == "pending" && time.Now().UTC().After(inv.ExpiresAt) {
		status = "expired"
	}
	resp := invitationResp{
		ID:            inv.ID,
		OrgID:         inv.OrgID,
		OrgSlug:       inv.OrgSlug,
		OrgDisplay:    inv.OrgDisplayName,
		InviteeUserID: inv.InviteeUserID,
		InviteeEmail:  inv.InviteeEmail,
		Role:          inv.Role,
		Status:        status,
		ExpiresAt:     inv.ExpiresAt,
		CreatedAt:     inv.CreatedAt,
		AcceptedAt:    inv.AcceptedAt,
	}
	if inv.Inviter != nil {
		resp.Inviter = &userRef{
			ID:          inv.Inviter.ID,
			Username:    inv.Inviter.Username,
			DisplayName: inv.Inviter.DisplayName,
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	if h.Hasher == nil {
		httpapi.RenderError(w, r, apperr.Internal(nil))
		return
	}
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	raw := strings.TrimSpace(chi.URLParam(r, "token"))
	if raw == "" {
		httpapi.RenderError(w, r, apperr.NotFound("invitation"))
		return
	}
	// We need the caller's email to allow the email-based accept match; load
	// the full user row up front so the store transaction stays simple.
	me, err := h.Store.GetUserByID(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	inv, err := h.Store.AcceptInvitation(r.Context(), userstore.AcceptInvitationParams{
		TokenHash: h.Hasher.Hash(raw),
		UserID:    id.UserID,
		UserEmail: me.Email,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, invitationResp{
		ID:            inv.ID,
		OrgID:         inv.OrgID,
		OrgSlug:       inv.OrgSlug,
		OrgDisplay:    inv.OrgDisplayName,
		InviteeUserID: inv.InviteeUserID,
		InviteeEmail:  inv.InviteeEmail,
		Role:          inv.Role,
		Status:        inv.Status,
		ExpiresAt:     inv.ExpiresAt,
		CreatedAt:     inv.CreatedAt,
		AcceptedAt:    inv.AcceptedAt,
	})
}

// buildInviteURL stitches together the share URL using InviteLinkBase (when
// set) or a sane "/invitations/{token}" default. The link target is the
// frontend route, not the API path.
func (h *Handler) buildInviteURL(rawToken string) string {
	base := strings.TrimRight(h.InviteLinkBase, "/")
	if base == "" {
		base = "/invitations"
	}
	return base + "/" + rawToken
}
