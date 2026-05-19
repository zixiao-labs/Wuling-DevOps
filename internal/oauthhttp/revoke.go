// oauthhttp/revoke.go — RFC 7009 OAuth 2.0 Token Revocation.
//
// Accepts either an access token or a refresh token in the `token` form
// field. The `token_type_hint` is optional and only a hint — we always check
// both. A revocation that doesn't match any row still returns 200, per the
// spec, so attackers can't probe for valid tokens.
package oauthhttp

import (
	"net/http"
)

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
	if row, err := h.OAuth.LookupAccessTokenByHash(r.Context(), hash); err == nil && row.RevokedAt == nil {
		_ = h.OAuth.RevokeAccessToken(r.Context(), row.ID)
		h.OAuth.Audit(r.Context(), "token_revoked",
			uuidPtr(row.UserID), uuidPtr(row.ClientID),
			map[string]any{"token_id": row.ID.String(), "via": "access"})
	} else if row, err := h.OAuth.LookupAccessTokenByRefreshHash(r.Context(), hash); err == nil && row.RevokedAt == nil {
		_ = h.OAuth.RevokeAccessToken(r.Context(), row.ID)
		h.OAuth.Audit(r.Context(), "token_revoked",
			uuidPtr(row.UserID), uuidPtr(row.ClientID),
			map[string]any{"token_id": row.ID.String(), "via": "refresh"})
	}

	// RFC 7009 §2.2: success means "the response is the same regardless of
	// whether the token was active or not". So always 200, body empty.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
}
