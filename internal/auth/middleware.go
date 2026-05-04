package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// Identity is the authenticated principal attached to every request.
type Identity struct {
	UserID   uuid.UUID
	Username string
	// Source describes how the request authenticated, for audit and for
	// behaviour gates (e.g. PATs may be scoped where JWTs are not).
	Source IdentitySource
	Scopes []string // populated when Source == IdentitySourcePAT
}

// IdentitySource discriminates between the auth methods.
type IdentitySource string

const (
	IdentitySourceJWT IdentitySource = "jwt"
	IdentitySourcePAT IdentitySource = "pat"
)

type identityCtxKey struct{}

// IdentityFromContext retrieves the principal stored by Middleware.
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(*Identity)
	return id, ok
}

// WithIdentity is exposed for tests and for the smart-HTTP handler that does
// its own basic-auth resolution.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// PATResolver resolves a raw PAT secret to the user it belongs to. The HTTP
// middleware uses this when a request presents Basic auth instead of a Bearer
// token, so the auth package itself doesn't depend on the DB layer.
type PATResolver interface {
	ResolvePAT(ctx context.Context, username, raw string) (*Identity, error)
}

// PasswordResolver resolves a username/password to the user. Used by the
// smart-HTTP handler to support `git clone https://user:pass@...` flows where
// users haven't issued a PAT yet.
type PasswordResolver interface {
	ResolvePassword(ctx context.Context, username, password string) (*Identity, error)
}

// Middleware enforces JWT bearer auth. If optional == true, requests without
// a token pass through with no identity attached; otherwise missing/invalid
// tokens get 401.
//
// The middleware does NOT handle Basic auth — that's used by the git smart
// HTTP handler which has its own resolver, since Bearer is the wrong shape
// for the Git CLI.
func Middleware(verifier *Verifier, optional bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if authz == "" {
				if optional {
					next.ServeHTTP(w, r)
					return
				}
				writeAuthError(w, apperr.Unauthorized("missing Authorization header"))
				return
			}
			const prefix = "Bearer "
			if !strings.HasPrefix(authz, prefix) {
				writeAuthError(w, apperr.Unauthorized("expected Bearer token"))
				return
			}
			tok := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
			claims, err := verifier.Verify(tok)
			if err != nil {
				writeAuthError(w, apperr.Wrap(apperr.CodeUnauthorized, "invalid or expired token", err))
				return
			}
			id := &Identity{
				UserID:   claims.UserID,
				Username: claims.Username,
				Source:   IdentitySourceJWT,
			}
			r = r.WithContext(WithIdentity(r.Context(), id))
			next.ServeHTTP(w, r)
		})
	}
}

// RequireIdentity is a small helper for handlers — returns the identity or a
// 401-rendered apperr for the response renderer.
func RequireIdentity(r *http.Request) (*Identity, error) {
	id, ok := IdentityFromContext(r.Context())
	if !ok {
		return nil, apperr.Unauthorized("authentication required")
	}
	return id, nil
}

// writeAuthError mirrors what the HTTP error renderer produces, but is local
// here to avoid an import cycle with internal/httpapi.
func writeAuthError(w http.ResponseWriter, e *apperr.Error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", `Bearer realm="wuling"`)
	w.WriteHeader(e.HTTPStatus())
	// Minimal JSON; the full renderer lives in internal/httpapi but this code
	// path is hot for git smart HTTP, so keep the dependency surface small.
	_, _ = w.Write([]byte(`{"error":{"code":"` + string(e.Code) + `","message":"` + jsonEscape(e.Message) + `"}}`))
}

func jsonEscape(s string) string {
	// Cheap escape — only quotes and backslashes appear in our messages today.
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return r.Replace(s)
}
