// oauthhttp/authorizations.go — endpoints for the user's "Authorized Apps"
// settings page. Users see every (user, client) consent row they hold and
// can revoke individual grants in one click; revoking also nukes the live
// tokens that grant minted.
package oauthhttp

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
)

func (h *Handler) listAuthorizations(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	rows, err := h.OAuth.ListAuthorizationsForUser(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	type view struct {
		ID            uuid.UUID `json:"id"`
		ClientID      string    `json:"client_id"`
		ClientName    string    `json:"client_name"`
		ClientLogoURL string    `json:"client_logo_url,omitempty"`
		IsFirstParty  bool      `json:"is_first_party"`
		Scopes        []string  `json:"scopes"`
		GrantedAt     string    `json:"granted_at"`
		UpdatedAt     string    `json:"updated_at"`
	}
	out := make([]view, 0, len(rows))
	for _, v := range rows {
		out = append(out, view{
			ID:            v.ID,
			ClientID:      v.ClientPublicID,
			ClientName:    v.ClientName,
			ClientLogoURL: v.ClientLogoURL,
			IsFirstParty:  v.IsFirstParty,
			Scopes:        v.Scopes,
			GrantedAt:     v.GrantedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt:     v.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) revokeAuthorization(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	authID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid id"))
		return
	}
	if err := h.OAuth.RevokeAuthorization(r.Context(), id.UserID, authID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	h.OAuth.Audit(r.Context(), "consent_revoked", uuidPtr(id.UserID), nil,
		map[string]any{"authorization_id": authID.String()})
	w.WriteHeader(http.StatusNoContent)
}
