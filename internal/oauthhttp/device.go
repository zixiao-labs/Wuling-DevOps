// oauthhttp/device.go — RFC 8628 Device Authorization Grant handlers.
//
// Three handlers cooperate:
//
//   POST /device_authorization   client requests a device+user code pair
//   POST /device/approve         user (logged in) approves a user_code
//   POST /device/deny            user (logged in) denies a user_code
//
// The polling exchange is in token.go (`grantDevice`).
package oauthhttp

import (
	"net/http"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/oauthstore"
)

// deviceAuthorization is POST /device_authorization. It accepts a
// form-urlencoded body (no auth) and returns the device-flow tuple.
func (h *Handler) deviceAuthorization(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
		return
	}
	clientIDStr := r.PostForm.Get("client_id")
	scopeParam := r.PostForm.Get("scope")
	if clientIDStr == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing client_id")
		return
	}
	client, err := h.OAuth.GetClientByClientID(r.Context(), clientIDStr)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client")
		return
	}
	// Device flow is only meaningful for public clients (RFC 8628 §1).
	if client.IsConfidential {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client",
			"device_authorization_grant is only available to public clients")
		return
	}
	scopes := parseScopeParam(scopeParam)
	if len(scopes) == 0 {
		scopes = client.DefaultScopes
	}
	if !allValidScopes(scopes) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_scope", "one or more scopes are not supported")
		return
	}

	rawDevice, deviceHash, err := auth.NewDeviceCode(h.Hasher)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not mint device_code")
		return
	}
	userCodeDisplay, userCodeRaw, err := auth.NewUserCode()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not mint user_code")
		return
	}

	if _, err := h.OAuth.CreateDeviceCode(r.Context(), oauthstore.CreateDeviceCodeParams{
		DeviceCodeHash: deviceHash,
		UserCode:       userCodeRaw,
		ClientID:       client.ID,
		Scopes:         scopes,
		IntervalSec:    int(h.DevicePollMin.Seconds()),
		TTL:            h.DeviceCodeTTL,
	}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist device_code")
		return
	}

	base := h.publicBaseURL(r)
	resp := struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}{
		DeviceCode:              rawDevice,
		UserCode:                userCodeDisplay,
		VerificationURI:         base + h.frontendURL("/oauth/device"),
		VerificationURIComplete: base + h.frontendURL("/oauth/device") + "?user_code=" + userCodeDisplay,
		ExpiresIn:               int(h.DeviceCodeTTL.Seconds()),
		Interval:                int(h.DevicePollMin.Seconds()),
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

// deviceApprove flips a pending device code to approved on behalf of the
// logged-in user. The SPA POSTs `{ "user_code": "ABCD-1234" }`.
func (h *Handler) deviceApprove(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var body struct {
		UserCode string `json:"user_code" validate:"required"`
	}
	if err := httpapi.DecodeJSON(w, r, &body); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	canonical := auth.NormalizeUserCode(body.UserCode)
	row, err := h.OAuth.GetDeviceCodeByUserCode(r.Context(), canonical)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if row.Status != "pending" {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeConflict, "device code already decided"))
		return
	}
	if h.Now().After(row.ExpiresAt) {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeConflict, "device code expired"))
		return
	}
	if err := h.OAuth.ApproveDeviceCode(r.Context(), canonical, id.UserID, row.Scopes); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if _, err := h.OAuth.UpsertAuthorization(r.Context(), id.UserID, row.ClientID, row.Scopes); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	h.OAuth.Audit(r.Context(), "consent_granted", uuidPtr(id.UserID), uuidPtr(row.ClientID),
		map[string]any{"flow": "device", "scopes": row.Scopes,
			"user_code_log": auth.MaskUserCode(canonical)})
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "approved"})
}

// deviceDeny is the same as deviceApprove but writes status=denied.
func (h *Handler) deviceDeny(w http.ResponseWriter, r *http.Request) {
	_, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var body struct {
		UserCode string `json:"user_code" validate:"required"`
	}
	if err := httpapi.DecodeJSON(w, r, &body); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	canonical := auth.NormalizeUserCode(body.UserCode)
	if err := h.OAuth.DenyDeviceCode(r.Context(), canonical); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "denied"})
}
