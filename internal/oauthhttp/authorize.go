// oauthhttp/authorize.go — Authorization Code + PKCE flow handlers.
//
// Three handlers cooperate:
//
//   1. GET  /authorize             validates inputs, stashes them server-side,
//                                  redirects browser to the SPA consent page
//                                  with only an opaque `req` id.
//   2. GET  /authorize/preview     SPA fetches consent metadata for ?req=.
//   3. POST /authorize/decision    SPA posts allow/deny; we mint an
//                                  authorization_code and redirect back to
//                                  the third-party redirect_uri.
//
// The split keeps the consent UI in React land and the protocol state on the
// server — so query-string tampering can't change the requested scopes, and
// browser history doesn't capture client_id/scope leaks.
package oauthhttp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/oauthstore"
)

const (
	consentCookieName    = "wuling_oauth_consent"
	consentCookieMaxAge  = 10 * 60 // 10 minutes
)

// authorize is GET /authorize. Quote from RFC 6749 §4.1.1:
//
//	response_type=code  REQUIRED
//	client_id           REQUIRED
//	redirect_uri        OPTIONAL (we make it required to defeat ambiguity)
//	scope               REQUIRED
//	state               RECOMMENDED (we make it required)
//	code_challenge      REQUIRED (we always enforce PKCE)
//	code_challenge_method = S256 (only one we accept)
//
// We deliberately don't accept `prompt`, `login_hint`, or `nonce` yet — they
// can be added when OIDC mode arrives.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	responseType := q.Get("response_type")
	clientIDStr := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	scopeParam := q.Get("scope")
	state := q.Get("state")
	challenge := q.Get("code_challenge")
	challengeMethod := q.Get("code_challenge_method")

	if responseType != "code" {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "response_type must be 'code'"))
		return
	}
	if clientIDStr == "" || redirectURI == "" || scopeParam == "" || state == "" || challenge == "" {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest,
			"missing required parameter (client_id, redirect_uri, scope, state, code_challenge)"))
		return
	}
	if challengeMethod == "" {
		challengeMethod = "S256"
	}
	if challengeMethod != "S256" {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "only S256 PKCE is supported"))
		return
	}

	client, err := h.OAuth.GetClientByClientID(r.Context(), clientIDStr)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !redirectURIAllowed(client.RedirectURIs, redirectURI) {
		// Do NOT redirect when the redirect_uri itself failed validation —
		// the user-agent might be sending us to an attacker. Render an
		// inline error instead. RFC 6749 §3.1.2.4.
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "redirect_uri is not registered for this client"))
		return
	}
	scopes := parseScopeParam(scopeParam)
	if !allValidScopes(scopes) {
		redirectWithErr(w, r, redirectURI, state, "invalid_scope", "one or more requested scopes are not supported")
		return
	}

	// Bind the consent to a one-shot CSRF cookie so the eventual decision
	// POST cannot be replayed from a different session.
	csrfNonce, err := randomNonce(32)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	cookieHash := hmacHex(h.Cfg.ProviderHMACSecret, csrfNonce)
	setCookie(w, r, consentCookieName, csrfNonce, consentCookieMaxAge)

	req, err := h.OAuth.CreateAuthRequest(r.Context(), oauthstore.CreateAuthRequestParams{
		ClientID:            client.ID,
		RedirectURI:         redirectURI,
		Scopes:              scopes,
		State:               state,
		CodeChallenge:       challenge,
		CodeChallengeMethod: challengeMethod,
		SessionCookieHash:   cookieHash,
		TTL:                 10 * time.Minute,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	// Hand the SPA an opaque id; client_id/scope/state never leave the server.
	http.Redirect(w, r, h.frontendURL("/oauth/authorize")+"?req="+req.ID.String(), http.StatusFound)
}

// authorizePreview is the consent-UI data fetch. Anonymous: the SPA renders
// the consent screen before the user logs in if they aren't already. (The
// decision endpoint is the one that demands JWT auth.)
func (h *Handler) authorizePreview(w http.ResponseWriter, r *http.Request) {
	reqID, err := uuid.Parse(r.URL.Query().Get("req"))
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid req parameter"))
		return
	}
	req, err := h.OAuth.GetAuthRequest(r.Context(), reqID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	client, err := h.OAuth.GetClientByID(r.Context(), req.ClientID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	resp := authorizePreviewResp{
		Req: reqID.String(),
		Client: clientPublicView{
			ClientID:      client.ClientID,
			Name:          client.Name,
			HomepageURL:   client.HomepageURL,
			Description:   client.Description,
			LogoURL:       client.LogoURL,
			IsFirstParty:  client.IsFirstParty,
		},
		ScopesRequested: req.Scopes,
		ExpiresAt:       req.ExpiresAt,
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

type authorizePreviewResp struct {
	Req             string            `json:"req"`
	Client          clientPublicView  `json:"client"`
	ScopesRequested []string          `json:"scopes_requested"`
	ExpiresAt       time.Time         `json:"expires_at"`
}

type clientPublicView struct {
	ClientID     string `json:"client_id"`
	Name         string `json:"name"`
	HomepageURL  string `json:"homepage_url,omitempty"`
	Description  string `json:"description,omitempty"`
	LogoURL      string `json:"logo_url,omitempty"`
	IsFirstParty bool   `json:"is_first_party"`
}

// authorizeDecision is POST /authorize/decision. The user is logged in via
// the JWT middleware; we validate the CSRF cookie, write the decision into
// the stored request, mint an authorization_code on allow, and return the
// redirect URL the SPA should send the browser to.
func (h *Handler) authorizeDecision(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var body struct {
		Req      string `json:"req"      validate:"required,uuid"`
		Decision string `json:"decision" validate:"required,oneof=allow deny"`
	}
	if err := httpapi.DecodeJSON(w, r, &body); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	reqID, _ := uuid.Parse(body.Req)

	ck, err := r.Cookie(consentCookieName)
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "missing consent cookie"))
		return
	}
	req, err := h.OAuth.GetAuthRequest(r.Context(), reqID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !hmac.Equal([]byte(hmacHex(h.Cfg.ProviderHMACSecret, ck.Value)), []byte(req.SessionCookieHash)) {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "consent cookie does not match this request"))
		return
	}

	req, err = h.OAuth.RecordAuthRequestDecision(r.Context(), reqID, id.UserID, body.Decision)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	clearCookie(w, r, consentCookieName)

	if body.Decision == "deny" {
		redirectURL := buildRedirectURL(req.RedirectURI, map[string]string{
			"error":             "access_denied",
			"error_description": "user denied the request",
			"state":             req.State,
		})
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"redirect_url": redirectURL})
		return
	}

	// Allow: persist the (user, client) consent for future requests, mint the
	// authorization code, return the redirect URL.
	if _, err := h.OAuth.UpsertAuthorization(r.Context(), id.UserID, req.ClientID, req.Scopes); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	rawCode, codeHash, err := auth.NewAuthCode(h.Hasher)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	if err := h.OAuth.CreateAuthCode(r.Context(), oauthstore.CreateAuthCodeParams{
		CodeHash:      codeHash,
		ClientID:      req.ClientID,
		UserID:        id.UserID,
		RedirectURI:   req.RedirectURI,
		Scopes:        req.Scopes,
		CodeChallenge: req.CodeChallenge,
		TTL:           h.AuthCodeTTL,
	}); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	h.OAuth.Audit(r.Context(), "consent_granted", uuidPtr(id.UserID), uuidPtr(req.ClientID),
		map[string]any{"scopes": req.Scopes})

	redirectURL := buildRedirectURL(req.RedirectURI, map[string]string{
		"code":  rawCode,
		"state": req.State,
	})
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"redirect_url": redirectURL})
}

// publicClient is GET /clients/{client_id}. Returns the user-facing metadata
// for a third-party app — the consent UI uses this to render the avatar /
// name / link, and Esperanta uses it to verify what server it's pointed at.
func (h *Handler) publicClient(w http.ResponseWriter, r *http.Request) {
	clientIDStr := chi.URLParam(r, "client_id")
	c, err := h.OAuth.GetClientByClientID(r.Context(), clientIDStr)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, clientPublicView{
		ClientID:     c.ClientID,
		Name:         c.Name,
		HomepageURL:  c.HomepageURL,
		Description:  c.Description,
		LogoURL:      c.LogoURL,
		IsFirstParty: c.IsFirstParty,
	})
}

// ---------- redirect_uri matching ----------

// redirectURIAllowed checks that `candidate` is registered for the client.
// Exact string equality is the rule, with one carve-out per RFC 8252 §7.3:
// a registered "http://127.0.0.1" (or "http://localhost") permits any port.
// This is how native desktop apps like Esperanta open a loopback HTTP server
// to receive the auth_code.
func redirectURIAllowed(registered []string, candidate string) bool {
	cu, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	for _, r := range registered {
		if r == candidate {
			return true
		}
		ru, err := url.Parse(r)
		if err != nil {
			continue
		}
		if !isLoopbackOrigin(ru) || !isLoopbackOrigin(cu) {
			continue
		}
		// Loopback rule: scheme + host (without port) + path must match;
		// port may vary.
		if ru.Scheme == cu.Scheme && ru.Hostname() == cu.Hostname() && ru.Path == cu.Path {
			return true
		}
	}
	return false
}

func isLoopbackOrigin(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// ---------- helpers ----------

func buildRedirectURL(base string, params map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func randomNonce(nBytes int) (string, error) {
	if nBytes < 16 {
		return "", errors.New("nonce too small")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hmacHex(secret, raw string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(raw))
	return hex.EncodeToString(m.Sum(nil))
}

func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	return &id
}
