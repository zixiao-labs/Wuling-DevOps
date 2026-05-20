// Package oauthhttp is the HTTP surface for the OAuth Authorization Server
// role: Wuling-DevOps minting tokens for its own first-party apps (Esperanta)
// and for third-party apps that users register.
//
// Where `authhttp` handles "log into THIS service using a different IdP"
// (e.g. Sign in with GitHub), `oauthhttp` handles "let some other app use a
// Wuling-DevOps identity on the user's behalf". The two packages share the
// same `userstore.User` and the same JWT issuer for the user's own session,
// but they live on different code paths so the audit and scope models stay
// distinct.
//
// Mount maps:
//
//   /api/v1/oauth/authorize             (GET)   start an Authorization Code flow
//   /api/v1/oauth/authorize/preview     (GET)   consent UI data fetch
//   /api/v1/oauth/authorize/decision    (POST)  consent UI decision
//   /api/v1/oauth/token                 (POST)  exchange code / refresh / device
//   /api/v1/oauth/device_authorization  (POST)  start a Device Authorization Grant
//   /api/v1/oauth/device/approve        (POST)  consent UI: approve a user_code
//   /api/v1/oauth/device/deny           (POST)  consent UI: deny a user_code
//   /api/v1/oauth/revoke                (POST)  RFC 7009
//   /api/v1/oauth/clients/{client_id}   (GET)   public client metadata
//   /api/v1/oauth/authorizations        (GET)   list user's granted apps
//   /api/v1/oauth/authorizations/{id}   (DELETE) revoke one
//   /api/v1/oauth/apps                  (GET/POST) user's own OAuth apps
//   /api/v1/oauth/apps/{id}             (PATCH/DELETE)
//   /api/v1/oauth/apps/{id}/reset-secret (POST)
//   /api/v1/admin/oauth/apps            (GET) admin: list every client
//   /api/v1/admin/oauth/apps/{id}       (PATCH/DELETE) admin: mutate
//
// /.well-known/wuling-clients lives at root (not under /api/v1) for IdP
// discovery — it's separately mounted by internal/server.
package oauthhttp

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/oauthstore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler bundles the dependencies every sub-handler shares.
type Handler struct {
	OAuth   *oauthstore.Store
	Users   *userstore.Store
	Issuer  *auth.Issuer
	Hasher  *auth.HMACHasher
	Cfg     config.OAuthConfig
	Now     func() time.Time // injectable for tests

	// Lifetimes are static for now; promote to config if operators ask.
	AuthCodeTTL    time.Duration
	AccessTokenTTL time.Duration
	RefreshTTL     time.Duration
	DeviceCodeTTL  time.Duration
	DevicePollMin  time.Duration
}

// New returns a Handler with sensible defaults.
func New(deps Handler) *Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.AuthCodeTTL == 0 {
		deps.AuthCodeTTL = 10 * time.Minute
	}
	if deps.AccessTokenTTL == 0 {
		deps.AccessTokenTTL = 2 * time.Hour
	}
	if deps.RefreshTTL == 0 {
		deps.RefreshTTL = 30 * 24 * time.Hour
	}
	if deps.DeviceCodeTTL == 0 {
		deps.DeviceCodeTTL = 15 * time.Minute
	}
	if deps.DevicePollMin == 0 {
		deps.DevicePollMin = 5 * time.Second
	}
	return &deps
}

// Mount registers the routes that don't require JWT auth. Routes that DO
// require an authenticated user (consent decision, app management,
// authorization revocation) are mounted via MountAuthed.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/authorize", h.authorize)
	r.Get("/authorize/preview", h.authorizePreview)
	r.Post("/token", h.token)
	r.Post("/device_authorization", h.deviceAuthorization)
	r.Post("/revoke", h.revoke)
	r.Get("/clients/{client_id}", h.publicClient)
}

// MountAuthed registers the routes that require a valid logged-in user. The
// surrounding router should have already applied `auth.Middleware`.
func (h *Handler) MountAuthed(r chi.Router) {
	r.Post("/authorize/decision", h.authorizeDecision)
	r.Post("/device/approve", h.deviceApprove)
	r.Post("/device/deny", h.deviceDeny)
	r.Get("/authorizations", h.listAuthorizations)
	r.Delete("/authorizations/{id}", h.revokeAuthorization)
	r.Get("/apps", h.listApps)
	r.Post("/apps", h.createApp)
	r.Patch("/apps/{id}", h.updateApp)
	r.Delete("/apps/{id}", h.deleteApp)
	r.Post("/apps/{id}/reset-secret", h.resetAppSecret)
}

// MountAdmin registers admin-only routes. Caller must wrap with both the JWT
// middleware and the admin guard.
func (h *Handler) MountAdmin(r chi.Router) {
	r.Get("/oauth/apps", h.adminListApps)
	r.Patch("/oauth/apps/{id}", h.adminUpdateApp)
	r.Delete("/oauth/apps/{id}", h.adminDeleteApp)
}

// publicBaseURL returns the absolute origin we advertise to OAuth clients.
// If WULING_OAUTH_PUBLIC_BASE_URL is set we use that; otherwise we fall back
// to the request's own scheme+host, which is good enough for dev and for any
// production deployment running behind a single hostname.
func (h *Handler) publicBaseURL(r *http.Request) string {
	if v := strings.TrimRight(h.Cfg.PublicBaseURL, "/"); v != "" {
		return v
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host
}

// frontendURL absolute-resolves a path against FrontendBaseURL. We re-use the
// helper shape from authhttp's GitHub OAuth flow so behaviour is identical.
func (h *Handler) frontendURL(path string) string {
	base := strings.TrimRight(h.Cfg.FrontendBaseURL, "/")
	if base == "" {
		base = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if base == "/" {
		return path
	}
	return base + path
}

// redirectWithErr writes a 302 back to redirect_uri with an OAuth-style error
// fragment ("?error=...&error_description=...&state=..."). RFC 6749 §4.1.2.1.
func redirectWithErr(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", code)
	if desc != "" {
		q.Set("error_description", desc)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// scopeSubset reports whether `have` ⊇ `need` — i.e. every element of need is
// also in have. Used to decide whether to skip the consent screen on a repeat
// /authorize.
func scopeSubset(have, need []string) bool {
	idx := make(map[string]struct{}, len(have))
	for _, s := range have {
		idx[s] = struct{}{}
	}
	for _, s := range need {
		if _, ok := idx[s]; !ok {
			return false
		}
	}
	return true
}

// normalizeScopes lower-cases, trims, dedupes a scope list and returns it
// sorted. Empty entries are dropped. The order is canonical so we can compare
// across calls; the OAuth spec doesn't mandate order.
func normalizeScopes(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			continue
		}
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	// Insertion-sort-equivalent quick sort via sort.Strings without import:
	// for n <= ~10 this is fine, but use sort.Strings for clarity.
	stringSliceSort(out)
	return out
}

func stringSliceSort(a []string) {
	// in-place insertion sort, small N
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// parseScopeParam splits an OAuth-style space- or comma-separated scope param.
func parseScopeParam(s string) []string {
	if s == "" {
		return nil
	}
	r := strings.ReplaceAll(s, ",", " ")
	parts := strings.Fields(r)
	return normalizeScopes(parts)
}

// SupportedScopes is the canonical list — kept in one place so the well-known
// document, the validation, and the consent UI agree.
var SupportedScopes = []string{
	"user:read", "user:write",
	"repo:read", "repo:write",
	"issue:read", "issue:write",
	"mr:read", "mr:write",
	"git:read", "git:write",
}

// allValidScopes returns true if every entry of in is in SupportedScopes.
func allValidScopes(in []string) bool {
	idx := make(map[string]struct{}, len(SupportedScopes))
	for _, s := range SupportedScopes {
		idx[s] = struct{}{}
	}
	for _, s := range in {
		if _, ok := idx[s]; !ok {
			return false
		}
	}
	return true
}
