// oauthhttp/revoke.go — RFC 7009 OAuth 2.0 Token Revocation.
//
// Accepts either an access token or a refresh token in the `token` form
// field. The `token_type_hint` is optional and only a hint — we always check
// both. A revocation that doesn't match any row still returns 200, per the
// spec, so attackers can't probe for valid tokens.
package oauthhttp

import (
	"net/http"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// isNotFoundError checks if the error is a NotFound error.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	appErr := apperr.As(err)
	return appErr != nil && appErr.Code == apperr.CodeNotFound
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
		return
	}
	raw := r.PostForm.Get("token")
	if raw == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing token")
		return
	}
	hash := h.Hasher.Hash(raw)

	// Try as access token first, then as refresh.
	if row, err := h.OAuth.LookupAccessTokenByHash(r.Context(), hash); err == nil {
		// Token found. Check if already revoked.
		if row.RevokedAt == nil {
			// Token is active, revoke it.
			if err := h.OAuth.RevokeAccessToken(r.Context(), row.ID); err != nil {
				// Internal error during revocation.
				writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not revoke token")
				return
			}
			h.OAuth.Audit(r.Context(), "token_revoked",
				uuidPtr(row.UserID), uuidPtr(row.ClientID),
				map[string]any{"token_id": row.ID.String(), "via": "access"})
		}
		// Already revoked or successfully revoked, return 200.
	} else if !isNotFoundError(err) {
		// Internal error during lookup.
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not lookup token")
		return
	} else if row, err := h.OAuth.LookupAccessTokenByRefreshHash(r.Context(), hash); err == nil {
		// Token found by refresh hash. Check if already revoked.
		if row.RevokedAt == nil {
			// Token is active, revoke it.
			if err := h.OAuth.RevokeAccessToken(r.Context(), row.ID); err != nil {
				// Internal error during revocation.
				writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not revoke token")
				return
			}
			h.OAuth.Audit(r.Context(), "token_revoked",
				uuidPtr(row.UserID), uuidPtr(row.ClientID),
				map[string]any{"token_id": row.ID.String(), "via": "refresh"})
		}
		// Already revoked or successfully revoked, return 200.
	} else if !isNotFoundError(err) {
		// Internal error during lookup.
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not lookup token")
		return
	}
	// Token not found or already revoked or successfully revoked.

	// RFC 7009 §2.2: success means "the response is the same regardless of
	// whether the token was active or not". So always 200, body empty.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
}
