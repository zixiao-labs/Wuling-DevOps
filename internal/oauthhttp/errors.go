// oauthhttp/errors.go — OAuth-spec error helpers used by token.go and the
// device flow. The shape is `{ "error": "...", "error_description": "..." }`
// per RFC 6749 §5.2 — distinct from the JSON envelope httpapi.RenderError
// emits for our internal API, since OAuth clients expect this exact shape.
package oauthhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = jsonEncode(w, map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

func jsonEncode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// errAuth wraps a plain string into an apperr-shaped Unauthorized error so
// the auth middleware's writeAuthError handles it.
func errAuth(msg string) error {
	return apperr.Unauthorized(msg)
}

// ensure errors import is used (for nil-aware checks elsewhere).
var _ = errors.New
