// Package authhttp — GitHub OAuth login flow (issue #15).
//
// The flow is the textbook OAuth-2.0-with-PKCE dance:
//
//   1. Browser hits /api/v1/auth/oauth/github/start
//        - We generate (state, code_verifier), store them in a short-lived
//          HMAC-signed HttpOnly+SameSite=Lax cookie, and 302 the browser to
//          GitHub's /authorize endpoint.
//        - PKCE is optional for GitHub OAuth apps (they don't enforce it like
//          a stricter IdP would) but we send it anyway: belt-and-suspenders
//          for code-injection mitigation, and free if/when we swap providers.
//
//   2. GitHub bounces back to /api/v1/auth/oauth/github/callback?code&state
//        - We verify the state cookie, exchange the code for a token using
//          the stored verifier, then GET /user to fetch the GitHub identity.
//        - Linking decision:
//            • github_user_id already linked → log in.
//            • email matches an existing local user → DON'T silently link;
//              stash the pending decision in a second signed cookie and
//              bounce the browser to /oauth/confirm-link on the frontend
//              where the user explicitly chooses "link" or "create new".
//            • no match → create a brand-new account with a username derived
//              from the GitHub login (collision-suffixed with digits).
//        - On success we issue a JWT identical to the password-login path
//          and redirect the browser to <frontend>/oauth/callback with the
//          token in the URL fragment (so it never appears in server logs).
//
// Cookies are signed (HMAC-SHA256) with the JWT secret, not encrypted: their
// contents are not sensitive (only random nonces and identifiers), but they
// must be tamper-evident so a forged state cookie can't satisfy the callback.
package authhttp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// GithubAuthorizeURL is the OAuth authorize endpoint. Exposed as a var so tests
// can point it at an httptest server.
var GithubAuthorizeURL = "https://github.com/login/oauth/authorize"

// GithubTokenURL is the OAuth token endpoint.
var GithubTokenURL = "https://github.com/login/oauth/access_token"

// GithubAPIBaseURL is the base for /user and /user/emails calls.
var GithubAPIBaseURL = "https://api.github.com"

// OAuthHandler bundles dependencies for the GitHub OAuth flow.
type OAuthHandler struct {
	Store  *userstore.Store
	Issuer *auth.Issuer
	Cfg    config.OAuthConfig
	Signup config.SignupConfig
	JWT    config.JWTConfig // for cookie signing only
	// HTTPClient is the client used for the back-channel token exchange and
	// /user lookups. Defaults to a 10-second client.
	HTTPClient *http.Client
}

// Mount registers the OAuth subroutes on the /api/v1/auth router.
func (h *OAuthHandler) Mount(r chi.Router) {
	r.Get("/oauth/github/start", h.start)
	r.Get("/oauth/github/callback", h.callback)
	r.Post("/oauth/github/confirm", h.confirm)
}

// IsConfigured reports whether the GitHub OAuth client was wired by env vars.
// Mount() still runs unconditionally — the handlers themselves render a clear
// "GitHub OAuth not configured" error rather than 404s, so misconfigured
// installs surface in logs instead of in a confusing UX.
func (h *OAuthHandler) IsConfigured() bool {
	return h.Cfg.GithubClientID != "" && h.Cfg.GithubClientSecret != "" && h.Cfg.GithubRedirectURL != ""
}

// ---------- cookies ----------

const (
	oauthStateCookie   = "wuling_oauth_state"
	oauthPendingCookie = "wuling_oauth_pending"
	oauthCookieMaxAge  = 10 * 60 // 10 minutes; OAuth roundtrips that don't finish in 10 min are almost certainly abandoned tabs.
)

// stateCookie is what we drop on /start and verify on /callback. The struct is
// JSON-encoded, then HMAC-signed; the cookie value is "<b64payload>.<b64sig>".
type stateCookie struct {
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	ReturnTo string `json:"r,omitempty"`
	Exp      int64  `json:"e"`
}

// pendingLinkCookie is dropped on the email-match branch of /callback. The
// confirm endpoint reads it back to decide who to link and lets us avoid
// stuffing GitHub identity into the URL where a careless user could leak it.
type pendingLinkCookie struct {
	GithubUserID    int64  `json:"gid"`
	GithubLogin     string `json:"glogin"`
	Email           string `json:"email"`
	CandidateUserID string `json:"cuid"`
	Exp             int64  `json:"e"`
}

func (h *OAuthHandler) signCookie(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(h.JWT.Secret))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

func (h *OAuthHandler) verifyCookie(value string, out any) error {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return errors.New("malformed cookie")
	}
	mac := hmac.New(sha256.New, []byte(h.JWT.Secret))
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(want)) != 1 {
		return errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func setSignedCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/api/v1/auth",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure when the request came in over HTTPS or a TLS-terminating
		// reverse proxy says so. Plain dev (http://localhost) deliberately
		// stays unsecure so the cookie works there too.
		Secure: isHTTPS(r),
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/api/v1/auth",
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
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

// ---------- handlers ----------

func (h *OAuthHandler) start(w http.ResponseWriter, r *http.Request) {
	if !h.IsConfigured() {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "GitHub OAuth is not configured on this server"))
		return
	}

	nonce, err := randomURLSafe(32)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	returnTo := r.URL.Query().Get("return_to")
	cookieValue, err := h.signCookie(stateCookie{
		Nonce:    nonce,
		Verifier: verifier,
		ReturnTo: returnTo,
		Exp:      time.Now().Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	setSignedCookie(w, r, oauthStateCookie, cookieValue, oauthCookieMaxAge)

	q := url.Values{}
	q.Set("client_id", h.Cfg.GithubClientID)
	q.Set("redirect_uri", h.Cfg.GithubRedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", strings.ReplaceAll(h.Cfg.GithubScopes, ",", " "))
	q.Set("state", nonce)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("allow_signup", "true")
	http.Redirect(w, r, GithubAuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

func (h *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
	if !h.IsConfigured() {
		h.redirectError(w, r, "unavailable", "GitHub OAuth is not configured on this server")
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectError(w, r, errParam, r.URL.Query().Get("error_description"))
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		h.redirectError(w, r, "bad_request", "missing code or state")
		return
	}

	ck, err := r.Cookie(oauthStateCookie)
	if err != nil {
		h.redirectError(w, r, "bad_request", "missing state cookie")
		return
	}
	clearCookie(w, r, oauthStateCookie)

	var st stateCookie
	if err := h.verifyCookie(ck.Value, &st); err != nil {
		h.redirectError(w, r, "bad_request", "invalid state cookie")
		return
	}
	if time.Now().Unix() > st.Exp {
		h.redirectError(w, r, "bad_request", "state cookie expired")
		return
	}
	if subtle.ConstantTimeCompare([]byte(st.Nonce), []byte(state)) != 1 {
		h.redirectError(w, r, "bad_request", "state mismatch")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	accessToken, err := h.exchangeCode(ctx, code, st.Verifier)
	if err != nil {
		h.redirectError(w, r, "bad_gateway", "GitHub token exchange failed: "+err.Error())
		return
	}

	ghUser, err := h.fetchGithubUser(ctx, accessToken)
	if err != nil {
		h.redirectError(w, r, "bad_gateway", "GitHub /user lookup failed: "+err.Error())
		return
	}

	// Path 1: GitHub identity already linked to a local user → log in.
	if u, err := h.Store.GetUserByGithubID(r.Context(), ghUser.ID); err == nil {
		h.finishLogin(w, r, u, st.ReturnTo, false)
		return
	} else if !isNotFound(err) {
		h.redirectError(w, r, "internal", "lookup user by github id failed")
		return
	}

	// Path 2: email matches an existing user → require an explicit
	// confirmation step before linking. We stash the pending state in a
	// signed cookie and bounce to the frontend confirm page.
	if ghUser.Email != "" {
		existing, err := h.Store.GetUserByEmail(r.Context(), strings.ToLower(ghUser.Email))
		if err == nil {
			pending, err := h.signCookie(pendingLinkCookie{
				GithubUserID:    ghUser.ID,
				GithubLogin:     ghUser.Login,
				Email:           strings.ToLower(ghUser.Email),
				CandidateUserID: existing.ID.String(),
				Exp:             time.Now().Add(10 * time.Minute).Unix(),
			})
			if err != nil {
				h.redirectError(w, r, "internal", "sign pending cookie failed")
				return
			}
			setSignedCookie(w, r, oauthPendingCookie, pending, oauthCookieMaxAge)
			http.Redirect(w, r, h.frontendURL("/oauth/confirm-link"), http.StatusFound)
			return
		} else if !isNotFound(err) {
			h.redirectError(w, r, "internal", "lookup user by email failed")
			return
		}
	}

	// Path 3: brand new account.
	user, err := h.createOAuthUser(r.Context(), ghUser)
	if err != nil {
		ae := apperr.As(err)
		if ae != nil {
			h.redirectError(w, r, string(ae.Code), ae.Message)
		} else {
			h.redirectError(w, r, "internal", "create user failed")
		}
		return
	}
	h.finishLogin(w, r, user, st.ReturnTo, true)
}

// confirm completes the email-match linking flow. The frontend POSTs here
// after the user clicks "Link" or "Create new account" on the confirm page.
//
// Body: {"action":"link"} or {"action":"new"}.
//
// Action=link merges the GitHub identity onto the existing local account.
// Action=new ignores the email match and creates a fresh local account with a
// dedupe-suffixed username — useful when GitHub-side email reuse would
// otherwise hijack a stranger's account.
func (h *OAuthHandler) confirm(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie(oauthPendingCookie)
	if err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "no pending OAuth link in progress"))
		return
	}
	var pl pendingLinkCookie
	if err := h.verifyCookie(ck.Value, &pl); err != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid pending link cookie"))
		return
	}
	if time.Now().Unix() > pl.Exp {
		clearCookie(w, r, oauthPendingCookie)
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "pending link expired — restart the OAuth flow"))
		return
	}

	var body struct {
		Action string `json:"action" validate:"required,oneof=link new"`
	}
	if err := httpapi.DecodeJSON(w, r, &body); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	clearCookie(w, r, oauthPendingCookie)

	var user *model.User
	isNewAccount := false
	switch body.Action {
	case "link":
		candidateID, err := uuid.Parse(pl.CandidateUserID)
		if err != nil {
			httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid pending link cookie"))
			return
		}
		if err := h.Store.LinkGithubAccount(r.Context(), candidateID, pl.GithubUserID, pl.GithubLogin); err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		u, err := h.Store.GetUserByID(r.Context(), candidateID)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		user = u
	case "new":
		gh := &githubUserInfo{
			ID:    pl.GithubUserID,
			Login: pl.GithubLogin,
			// Deliberately drop the email — picking "create new" means the
			// caller refused to claim the existing email-matched account, so
			// we can't safely store it on the new row (the unique index would
			// reject it anyway).
			Email: "",
		}
		u, err := h.createOAuthUser(r.Context(), gh)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		user = u
		isNewAccount = true
	}

	if blockErr := h.checkApproval(user); blockErr != nil {
		httpapi.WriteJSON(w, http.StatusAccepted, oauthPendingResp{
			Status:  user.ApprovalStatus,
			Message: blockErr.Message,
			User:    user,
		})
		return
	}

	tok, exp, err := h.Issuer.Issue(user.ID, user.Username)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, oauthConfirmResp{
		tokenResp: tokenResp{
			AccessToken: tok,
			TokenType:   "Bearer",
			ExpiresAt:   exp,
			User:        user,
		},
		NewAccount: isNewAccount,
	})
}

type oauthPendingResp struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	User    *model.User `json:"user"`
}

type oauthConfirmResp struct {
	tokenResp
	NewAccount bool `json:"new_account"`
}

// ---------- helpers ----------

// finishLogin issues a JWT for the resolved user and redirects the browser to
// the frontend with the credentials in the URL fragment. Fragments aren't
// sent to the server by the browser, so the token never appears in our access
// logs — matching how SPAs typically receive OAuth callbacks.
func (h *OAuthHandler) finishLogin(w http.ResponseWriter, r *http.Request, user *model.User, returnTo string, newAccount bool) {
	if blockErr := h.checkApproval(user); blockErr != nil {
		v := url.Values{}
		v.Set("pending_approval", "1")
		v.Set("status", user.ApprovalStatus)
		v.Set("username", user.Username)
		http.Redirect(w, r, h.frontendURL("/oauth/callback")+"#"+v.Encode(), http.StatusFound)
		return
	}

	tok, exp, err := h.Issuer.Issue(user.ID, user.Username)
	if err != nil {
		h.redirectError(w, r, "internal", "issue jwt failed")
		return
	}
	v := url.Values{}
	v.Set("access_token", tok)
	v.Set("expires_at", exp.UTC().Format(time.RFC3339))
	if newAccount {
		v.Set("new_account", "1")
	}
	if returnTo != "" {
		v.Set("return_to", returnTo)
	}
	http.Redirect(w, r, h.frontendURL("/oauth/callback")+"#"+v.Encode(), http.StatusFound)
}

func (h *OAuthHandler) redirectError(w http.ResponseWriter, r *http.Request, code, message string) {
	v := url.Values{}
	v.Set("error", code)
	v.Set("error_description", message)
	http.Redirect(w, r, h.frontendURL("/oauth/callback")+"#"+v.Encode(), http.StatusFound)
}

func (h *OAuthHandler) frontendURL(path string) string {
	base := strings.TrimRight(h.Cfg.FrontendBaseURL, "/")
	if base == "" {
		base = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// "/" + "/foo" → "/foo"; "https://x" + "/foo" → "https://x/foo"
	if base == "/" {
		return path
	}
	return base + path
}

// checkApproval returns nil when the user is allowed to log in, or an
// *apperr.Error describing why not.
func (h *OAuthHandler) checkApproval(user *model.User) *apperr.Error {
	if !user.IsActive {
		return apperr.Forbidden("account is disabled")
	}
	switch user.ApprovalStatus {
	case model.UserApprovalPending:
		return apperr.New(apperr.CodeForbidden, "account is pending admin approval")
	case model.UserApprovalRejected:
		return apperr.New(apperr.CodeForbidden, "account registration was rejected")
	}
	return nil
}

// createOAuthUser inserts a new local user for the given GitHub identity.
// Username is derived from the GitHub login with a digit suffix on collision.
func (h *OAuthHandler) createOAuthUser(ctx context.Context, gh *githubUserInfo) (*model.User, error) {
	username, err := h.uniqueUsername(ctx, gh.Login)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(gh.Email))
	if email == "" {
		// GitHub may withhold email when the user keeps it private and we
		// didn't request the user:email scope. Fall back to a placeholder
		// that satisfies the NOT NULL + UNIQUE constraints. Users can fix it
		// from the profile settings later.
		email = fmt.Sprintf("%s+%d@users.noreply.github.com", username, gh.ID)
	}
	approval := model.UserApprovalPending
	if !h.Signup.RequireApproval || h.Signup.AutoApproveOAuth {
		approval = model.UserApprovalApproved
	}
	ghID := gh.ID
	u, _, err := h.Store.CreateUser(ctx, userstore.CreateUserParams{
		Username:       username,
		Email:          email,
		DisplayName:    defaultDisplayName(gh, username),
		PasswordHash:   "", // OAuth-only.
		GithubUserID:   &ghID,
		GithubLogin:    gh.Login,
		ApprovalStatus: approval,
	})
	return u, err
}

func defaultDisplayName(gh *githubUserInfo, fallback string) string {
	if strings.TrimSpace(gh.Name) != "" {
		return gh.Name
	}
	return fallback
}

// uniqueUsername normalises the GitHub login into something usable as a local
// username (letters, digits, `_`, `-`, starting with a letter) and appends a
// digit suffix until the result is unused. Worst case bounded by 1000 tries —
// far beyond any realistic collision rate.
func (h *OAuthHandler) uniqueUsername(ctx context.Context, ghLogin string) (string, error) {
	base := sanitizeUsername(ghLogin)
	if base == "" {
		base = "user"
	}
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s%d", base, i+1)
		}
		exists, err := h.Store.UsernameExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", apperr.Conflict("could not allocate a unique username after 1000 attempts")
}

// sanitizeUsername converts a GitHub login into a string that the local
// "alphanumdash" username validator will accept. GitHub logins are already
// close (letters, digits, `-`), but we still defend against edge cases like
// leading digits, dots, or unicode that someone might smuggle in.
func sanitizeUsername(in string) string {
	in = strings.TrimSpace(in)
	var b strings.Builder
	for i, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case (r >= '0' && r <= '9') || r == '_' || r == '-':
			if i == 0 {
				continue // must start with a letter
			}
			b.WriteRune(r)
		case unicode.IsLetter(r):
			// Strip non-ASCII letters — keep things ASCII for git over SSH.
		}
		if b.Len() >= 64 {
			break
		}
	}
	return b.String()
}

// ---------- GitHub HTTP ----------

type githubUserInfo struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// exchangeCode trades the authorization code for a user access token using the
// stored PKCE verifier.
func (h *OAuthHandler) exchangeCode(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("client_id", h.Cfg.GithubClientID)
	form.Set("client_secret", h.Cfg.GithubClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", h.Cfg.GithubRedirectURL)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GithubTokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := h.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d: %s", res.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		AccessToken      string `json:"access_token"`
		Scope            string `json:"scope"`
		TokenType        string `json:"token_type"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("github: %s: %s", parsed.Error, parsed.ErrorDescription)
	}
	if parsed.AccessToken == "" {
		return "", errors.New("github returned no access token")
	}
	return parsed.AccessToken, nil
}

// fetchGithubUser pulls /user and (if the primary email is hidden there)
// follows up with /user/emails to surface a usable email address.
func (h *OAuthHandler) fetchGithubUser(ctx context.Context, accessToken string) (*githubUserInfo, error) {
	user := &githubUserInfo{}
	if err := h.githubGet(ctx, accessToken, "/user", user); err != nil {
		return nil, err
	}
	if user.Email == "" {
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if err := h.githubGet(ctx, accessToken, "/user/emails", &emails); err == nil {
			for _, e := range emails {
				if e.Primary && e.Verified {
					user.Email = e.Email
					break
				}
			}
		}
	}
	return user, nil
}

func (h *OAuthHandler) githubGet(ctx context.Context, accessToken, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GithubAPIBaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	res, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return fmt.Errorf("github %s -> %d: %s", path, res.StatusCode, truncate(string(body), 200))
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (h *OAuthHandler) client() *http.Client {
	if h.HTTPClient != nil {
		return h.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// ---------- misc ----------

func randomURLSafe(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func isNotFound(err error) bool {
	e := apperr.As(err)
	return e != nil && e.Code == apperr.CodeNotFound
}
