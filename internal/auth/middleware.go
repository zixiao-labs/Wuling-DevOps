package auth

import (
	"context"
	"encoding/json"
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
	Scopes []string // populated when Source ∈ {PAT, OAT}
	// ClientID is set when Source == OAT — the oauth_clients row that minted
	// the bearer. Empty for PAT/JWT.
	ClientID uuid.UUID
}

// IdentitySource discriminates between the auth methods.
type IdentitySource string

const (
	IdentitySourceJWT      IdentitySource = "jwt"
	IdentitySourcePAT      IdentitySource = "pat"
	IdentitySourceOAT      IdentitySource = "oat"
	IdentitySourcePassword IdentitySource = "password"
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

// OATResolver resolves a raw OAuth access token (wloat_…) to the principal
// the issuing /token call bound it to. Loaded scopes are the ones granted at
// issuance, not the client's `default_scopes` — that distinction matters when
// a user revokes one scope without revoking the whole grant.
type OATResolver interface {
	ResolveOAT(ctx context.Context, raw string) (*Identity, error)
}

// BearerResolver dispatches a Bearer token to the right backing resolver
// based on the token's prefix. JWTs (no prefix) hit Verifier; OATs hit OAT.
// This is what `Middleware` and the git smart-HTTP handler share so both code
// paths accept both bearer shapes.
type BearerResolver struct {
	JWT *Verifier
	OAT OATResolver
}

// Resolve inspects raw and routes to JWT.Verify or OAT.Resolve. Returns the
// resolved Identity, or an apperr-shaped error suitable for writeAuthError.
func (b BearerResolver) Resolve(ctx context.Context, raw string) (*Identity, error) {
	if IsOATShaped(raw) {
		if b.OAT == nil {
			return nil, apperr.Unauthorized("OAuth tokens not accepted by this server")
		}
		return b.OAT.ResolveOAT(ctx, raw)
	}
	claims, err := b.JWT.Verify(raw)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnauthorized, "invalid or expired token", err)
	}
	return &Identity{
		UserID:   claims.UserID,
		Username: claims.Username,
		Source:   IdentitySourceJWT,
	}, nil
}

// Middleware enforces Bearer auth using only JWTs. Existing internal API
// handlers that do not need to accept OAuth-issued access tokens (most of
// them — admin actions, account self-service, etc.) keep this signature.
// `optional == true` lets unauthenticated requests pass through.
//
// The middleware does NOT handle Basic auth — that's used by the git smart
// HTTP handler which has its own resolver, since Bearer is the wrong shape
// for the Git CLI.
func Middleware(verifier *Verifier, optional bool) func(http.Handler) http.Handler {
	return MiddlewareBearer(BearerResolver{JWT: verifier}, optional)
}

// MiddlewareBearer is the OAuth-aware variant. It dispatches Bearer tokens
// to either the JWT verifier or the OAT resolver based on the token prefix,
// so handlers that should accept third-party OAuth bearers (anything reading
// or mutating repo / issue / mr state) mount this instead of `Middleware`.
func MiddlewareBearer(resolver BearerResolver, optional bool) func(http.Handler) http.Handler {
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
			id, err := resolver.Resolve(r.Context(), tok)
			if err != nil {
				if ae := apperr.As(err); ae != nil {
					writeAuthError(w, ae)
				} else {
					writeAuthError(w, apperr.Wrap(apperr.CodeUnauthorized, "auth failed", err))
				}
				return
			}
			r = r.WithContext(WithIdentity(r.Context(), id))
			next.ServeHTTP(w, r)
		})
	}
}

// RequireScope blocks the wrapped handler unless the request's identity holds
// every scope in `needed`. JWT identities (interactive sessions) bypass the
// check — they represent a fully-trusted user-agent, not a delegated bearer.
// PAT and OAT identities are gated; a missing scope produces 403 with
// `insufficient_scope`.
func RequireScope(needed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := IdentityFromContext(r.Context())
			if !ok {
				writeAuthError(w, apperr.Unauthorized("authentication required"))
				return
			}
			if id.Source == IdentitySourceJWT {
				next.ServeHTTP(w, r)
				return
			}
			for _, want := range needed {
				if !hasScope(id.Scopes, want) {
					writeAuthError(w, apperr.New(apperr.CodeForbidden,
						"token missing required scope: "+want))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hasScope(have []string, want string) bool {
	for _, s := range have {
		if s == want {
			return true
		}
	}
	return false
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
	// Use encoding/json so all JSON control characters (U+0000..U+001F),
	// quotes, and backslashes are escaped correctly. json.Marshal of a string
	// returns a quoted JSON literal; strip the surrounding quotes since the
	// caller already provides them.
	b, err := json.Marshal(s)
	if err != nil {
		// Fall back to a conservative replacer; json.Marshal of a string
		// effectively cannot fail, but stay safe.
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
		return r.Replace(s)
	}
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return ""
}
