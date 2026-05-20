// oauthhttp/apps.go — user-facing OAuth App management endpoints. Apps are
// just oauth_clients rows whose owner_user_id is the logged-in user.
package oauthhttp

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/oauthstore"
)

// appView is the JSON shape returned to the owner; client_secret never
// appears here (only on create/reset, in createAppResp).
type appView struct {
	ID             uuid.UUID `json:"id"`
	ClientID       string    `json:"client_id"`
	Name           string    `json:"name"`
	HomepageURL    string    `json:"homepage_url"`
	Description    string    `json:"description"`
	LogoURL        string    `json:"logo_url"`
	IsFirstParty   bool      `json:"is_first_party"`
	IsConfidential bool      `json:"is_confidential"`
	RedirectURIs   []string  `json:"redirect_uris"`
	DefaultScopes  []string  `json:"default_scopes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toAppView(c *oauthstore.Client) appView {
	return appView{
		ID: c.ID, ClientID: c.ClientID, Name: c.Name, HomepageURL: c.HomepageURL,
		Description: c.Description, LogoURL: c.LogoURL,
		IsFirstParty: c.IsFirstParty, IsConfidential: c.IsConfidential,
		RedirectURIs: c.RedirectURIs, DefaultScopes: c.DefaultScopes,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

type createAppReq struct {
	Name           string   `json:"name"            validate:"required,min=1,max=128"`
	HomepageURL    string   `json:"homepage_url"    validate:"omitempty,url,max=512"`
	Description    string   `json:"description"     validate:"max=512"`
	LogoURL        string   `json:"logo_url"        validate:"omitempty,url,max=512"`
	IsConfidential bool     `json:"is_confidential"`
	RedirectURIs   []string `json:"redirect_uris"   validate:"required,min=1,max=8,dive,url,max=512"`
	DefaultScopes  []string `json:"default_scopes"  validate:"required,min=1,max=20"`
}

type createAppResp struct {
	App          appView `json:"app"`
	ClientID     string  `json:"client_id"`
	ClientSecret string  `json:"client_secret,omitempty"` // present only when is_confidential
}

func (h *Handler) listApps(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	clients, err := h.OAuth.ListClientsForOwner(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	out := make([]appView, 0, len(clients))
	for i := range clients {
		out = append(out, toAppView(&clients[i]))
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) createApp(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createAppReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !allValidScopes(req.DefaultScopes) {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "one or more default_scopes are not supported"))
		return
	}

	clientIDStr, err := randomClientID()
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	var secretHash *string
	var rawSecret string
	if req.IsConfidential {
		s, hash, err := newPrefixedSecret(h.Hasher, "wlocs_", 32)
		if err != nil {
			httpapi.RenderError(w, r, apperr.Internal(err))
			return
		}
		rawSecret = s
		secretHash = &hash
	}
	uid := id.UserID
	c, err := h.OAuth.CreateClient(r.Context(), oauthstore.CreateClientParams{
		ClientID:         clientIDStr,
		ClientSecretHash: secretHash,
		Name:             req.Name,
		HomepageURL:      req.HomepageURL,
		Description:      req.Description,
		LogoURL:          req.LogoURL,
		OwnerUserID:      &uid,
		IsFirstParty:     false,
		IsConfidential:   req.IsConfidential,
		RedirectURIs:     req.RedirectURIs,
		DefaultScopes:    normalizeScopes(req.DefaultScopes),
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, createAppResp{
		App:          toAppView(c),
		ClientID:     c.ClientID,
		ClientSecret: rawSecret,
	})
}

type updateAppReq struct {
	Name          *string   `json:"name"           validate:"omitempty,min=1,max=128"`
	HomepageURL   *string   `json:"homepage_url"   validate:"omitempty,url,max=512"`
	Description   *string   `json:"description"    validate:"omitempty,max=512"`
	LogoURL       *string   `json:"logo_url"       validate:"omitempty,url,max=512"`
	RedirectURIs  *[]string `json:"redirect_uris"  validate:"omitempty,min=1,max=8,dive,url,max=512"`
	DefaultScopes *[]string `json:"default_scopes" validate:"omitempty,min=1,max=20"`
}

func (h *Handler) updateApp(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	appID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid id"))
		return
	}
	var req updateAppReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if req.DefaultScopes != nil && !allValidScopes(*req.DefaultScopes) {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "one or more default_scopes are not supported"))
		return
	}
	if req.DefaultScopes != nil {
		normalized := normalizeScopes(*req.DefaultScopes)
		req.DefaultScopes = &normalized
	}
	uid := id.UserID
	if err := h.OAuth.UpdateClient(r.Context(), appID, &uid, oauthstore.UpdateClientParams{
		Name:          req.Name,
		HomepageURL:   req.HomepageURL,
		Description:   req.Description,
		LogoURL:       req.LogoURL,
		RedirectURIs:  req.RedirectURIs,
		DefaultScopes: req.DefaultScopes,
	}); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	c, err := h.OAuth.GetClientByID(r.Context(), appID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, toAppView(c))
}

func (h *Handler) deleteApp(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	appID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid id"))
		return
	}
	uid := id.UserID
	if err := h.OAuth.DeleteClient(r.Context(), appID, &uid); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resetAppSecret(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	appID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid id"))
		return
	}
	rawSecret, hash, err := newPrefixedSecret(h.Hasher, "wlocs_", 32)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	uid := id.UserID
	if err := h.OAuth.RotateClientSecret(r.Context(), appID, &uid, hash); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"client_secret": rawSecret})
}

// ---------- admin ----------

func (h *Handler) adminListApps(w http.ResponseWriter, r *http.Request) {
	clients, err := h.OAuth.ListAllClients(r.Context())
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	out := make([]appView, 0, len(clients))
	for i := range clients {
		out = append(out, toAppView(&clients[i]))
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) adminUpdateApp(w http.ResponseWriter, r *http.Request) {
	appID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid id"))
		return
	}
	var req struct {
		IsFirstParty *bool `json:"is_first_party"`
	}
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := h.OAuth.UpdateClient(r.Context(), appID, nil, oauthstore.UpdateClientParams{
		IsFirstParty: req.IsFirstParty,
	}); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	c, err := h.OAuth.GetClientByID(r.Context(), appID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, toAppView(c))
}

func (h *Handler) adminDeleteApp(w http.ResponseWriter, r *http.Request) {
	appID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid id"))
		return
	}
	if err := h.OAuth.DeleteClient(r.Context(), appID, nil); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
