// oauthhttp/well_known.go — discovery document advertised at
// /.well-known/wuling-clients. It lets a client (Esperanta in particular)
// look up the official desktop client_id and the endpoint URLs without
// being told them out of band, so self-hosted deployments work alongside
// the SaaS instance without a config flag.
package oauthhttp

import (
	"net/http"

	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
)

// WellKnownHandler renders the discovery JSON. Mounted at the absolute path
// `/.well-known/wuling-clients` by the surrounding server.
func (h *Handler) WellKnownHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := h.publicBaseURL(r)
		resp := map[string]any{
			"issuer":                          base,
			"desktop_official_client_id":      h.Cfg.DesktopClientID,
			"authorization_endpoint":          base + "/api/v1/oauth/authorize",
			"token_endpoint":                  base + "/api/v1/oauth/token",
			"device_authorization_endpoint":   base + "/api/v1/oauth/device_authorization",
			"revocation_endpoint":             base + "/api/v1/oauth/revoke",
			"frontend_device_verification_uri": h.absoluteFrontendURL(r, "/oauth/device"),
			"scopes_supported":                SupportedScopes,
			"response_types_supported":        []string{"code"},
			"grant_types_supported": []string{
				"authorization_code",
				"refresh_token",
				deviceCodeGrant,
			},
			"code_challenge_methods_supported": []string{"S256"},
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		httpapi.WriteJSON(w, http.StatusOK, resp)
	}
}
