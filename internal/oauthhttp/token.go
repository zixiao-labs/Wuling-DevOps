// oauthhttp/token.go — POST /token. Dispatches by grant_type:
//
//   authorization_code
//   refresh_token
//   urn:ietf:params:oauth:grant-type:device_code
//
// RFC 6749 form-urlencoded body; we don't accept JSON here so the
// standard `oauth2` client libraries Just Work.
package oauthhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/oauthstore"
)

const (
	deviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"
)

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
		return
	}
	grant := r.PostForm.Get("grant_type")

	// Confidential clients send their secret in either the Authorization
	// header (Basic auth) or in form fields. We handle both.
	clientIDStr, clientSecret := extractClientCredentials(r)
	if clientIDStr == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "missing client_id")
		return
	}
	client, err := h.OAuth.GetClientByClientID(r.Context(), clientIDStr)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client")
		return
	}
	if client.IsConfidential {
		if client.ClientSecretHash == nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "client misconfigured")
			return
		}
		if !h.Hasher.Equal(h.Hasher.Hash(clientSecret), *client.ClientSecretHash) {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "bad client_secret")
			return
		}
	} else if clientSecret != "" {
		// A public client presenting a secret is a misconfiguration we'd
		// rather reject loudly than silently accept.
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "public clients must not present client_secret")
		return
	}

	switch grant {
	case "authorization_code":
		h.grantAuthCode(w, r, client)
	case "refresh_token":
		h.grantRefresh(w, r, client)
	case deviceCodeGrant:
		h.grantDevice(w, r, client)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type not supported")
	}
}

// grantAuthCode handles `grant_type=authorization_code`. PKCE-validates,
// consumes the code, and issues an access+refresh pair.
func (h *Handler) grantAuthCode(w http.ResponseWriter, r *http.Request, client *oauthstore.Client) {
	rawCode := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")
	codeVerifier := r.PostForm.Get("code_verifier")
	if rawCode == "" || redirectURI == "" || codeVerifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing code, redirect_uri, or code_verifier")
		return
	}
	codeHash := h.Hasher.Hash(rawCode)
	row, err := h.OAuth.ConsumeAuthCode(r.Context(), codeHash)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if row.ClientID != client.ID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code was issued to a different client")
		return
	}
	if row.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match")
		return
	}
	if err := auth.PKCEVerify(codeVerifier, row.CodeChallenge); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	if err := h.issueAndRespond(w, r, client.ID, row.UserID, row.Scopes, nil, nil); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
	}
}

// grantRefresh handles `grant_type=refresh_token` with rotation + reuse
// detection. RFC 6819 §5.2.2.3.
func (h *Handler) grantRefresh(w http.ResponseWriter, r *http.Request, client *oauthstore.Client) {
	rawRefresh := r.PostForm.Get("refresh_token")
	if rawRefresh == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing refresh_token")
		return
	}
	refreshHash := h.Hasher.Hash(rawRefresh)
	row, err := h.OAuth.LookupAccessTokenByRefreshHash(r.Context(), refreshHash)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid")
		return
	}
	if row.ClientID != client.ID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token was issued to a different client")
		return
	}
	if row.RevokedAt != nil {
		// A revoked row whose hash is being presented again is the canonical
		// signal of a leak: revoke the whole chain and audit.
		if row.RefreshChainID != nil {
			_ = h.OAuth.RevokeChain(r.Context(), *row.RefreshChainID)
		}
		h.OAuth.Audit(r.Context(), "refresh_reuse_detected",
			uuidPtr(row.UserID), uuidPtr(row.ClientID), map[string]any{
				"token_id": row.ID.String(),
				"chain_id": chainIDStr(row.RefreshChainID),
			})
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token already used")
		return
	}
	if row.RefreshExpiresAt != nil && row.RefreshExpiresAt.Before(h.Now()) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	}

	// Rotate: revoke the old row, issue a new row in the same chain.
	if err := h.OAuth.RevokeAccessToken(r.Context(), row.ID); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not revoke old token")
		return
	}
	chainID := row.RefreshChainID
	parent := &refreshHash
	if err := h.issueAndRespond(w, r, client.ID, row.UserID, row.Scopes, chainID, parent); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
	}
}

// grantDevice handles RFC 8628 device_code exchange.
func (h *Handler) grantDevice(w http.ResponseWriter, r *http.Request, client *oauthstore.Client) {
	rawDevice := r.PostForm.Get("device_code")
	if rawDevice == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing device_code")
		return
	}
	hash := h.Hasher.Hash(rawDevice)
	row, err := h.OAuth.GetDeviceCodeByHash(r.Context(), hash)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "device_code unknown")
		return
	}
	if row.ClientID != client.ID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "device_code was issued to a different client")
		return
	}
	if h.Now().After(row.ExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "expired_token", "device_code expired")
		return
	}
	// Polling rate enforcement: bump status if too-frequent.
	if row.LastPolledAt != nil && h.Now().Sub(*row.LastPolledAt) < time.Duration(row.IntervalSec)*time.Second {
		writeOAuthError(w, http.StatusBadRequest, "slow_down", "polling too often")
		// Still touch so subsequent polls re-set the timer.
		h.OAuth.TouchDevicePoll(r.Context(), hash)
		return
	}
	h.OAuth.TouchDevicePoll(r.Context(), hash)

	switch row.Status {
	case "pending":
		writeOAuthError(w, http.StatusBadRequest, "authorization_pending", "user has not yet approved")
		return
	case "denied":
		writeOAuthError(w, http.StatusBadRequest, "access_denied", "user denied the request")
		return
	case "expired":
		writeOAuthError(w, http.StatusBadRequest, "expired_token", "device_code expired")
		return
	case "approved":
		// fallthrough below
	default:
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "unknown device_code status")
		return
	}
	if row.UserID == nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "approved device_code has no user")
		return
	}
	if err := h.issueAndRespond(w, r, client.ID, *row.UserID, row.Scopes, nil, nil); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
	}
}

// issueAndRespond mints an access+refresh pair, persists, audits, and writes
// the RFC 6749 §5.1 token response. `chainID` and `parentRefreshHash` are
// set on refresh rotations (nil on initial issuance).
func (h *Handler) issueAndRespond(
	w http.ResponseWriter, r *http.Request,
	clientID, userID uuid.UUID, scopes []string,
	chainID *uuid.UUID, parentRefreshHash *string,
) error {
	rawAccess, accessHash, err := auth.NewOAT(h.Hasher)
	if err != nil {
		return err
	}
	rawRefresh, refreshHash, err := auth.NewRefreshToken(h.Hasher)
	if err != nil {
		return err
	}
	row, err := h.OAuth.CreateAccessToken(r.Context(), oauthstore.CreateAccessTokenParams{
		UserID:            userID,
		ClientID:          clientID,
		TokenHash:         accessHash,
		Scopes:            scopes,
		TokenTTL:          h.AccessTokenTTL,
		RefreshTokenHash:  &refreshHash,
		RefreshTTL:        h.RefreshTTL,
		RefreshChainID:    chainID,
		ParentRefreshHash: parentRefreshHash,
	})
	if err != nil {
		return err
	}
	h.OAuth.Audit(r.Context(), "token_issued",
		uuidPtr(userID), uuidPtr(clientID),
		map[string]any{"token_id": row.ID.String(), "scopes": scopes})

	resp := tokenResp{
		AccessToken:  rawAccess,
		TokenType:    "Bearer",
		ExpiresIn:    int(h.AccessTokenTTL.Seconds()),
		RefreshToken: rawRefresh,
		Scope:        strings.Join(scopes, " "),
	}
	writeTokenResp(w, resp)
	return nil
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// writeTokenResp encodes the success body. RFC 6749 §5.1 requires
// Cache-Control: no-store and Pragma: no-cache.
func writeTokenResp(w http.ResponseWriter, body tokenResp) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = jsonEncode(w, body)
}

// extractClientCredentials pulls (client_id, client_secret) from either
// HTTP Basic auth or form fields. Returns empties if neither is present.
func extractClientCredentials(r *http.Request) (string, string) {
	if u, p, ok := r.BasicAuth(); ok {
		return u, p
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

// chainIDStr renders a nil-safe chain id for audit metadata.
func chainIDStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// ResolveOAT implements auth.OATResolver — looks up a `wloat_` bearer.
// Returns the active identity or an Unauthorized error if the token is
// unknown, expired, or revoked.
func (h *Handler) ResolveOAT(ctx context.Context, raw string) (*auth.Identity, error) {
	if !auth.IsOATShaped(raw) {
		return nil, errors.New("not an OAT")
	}
	hash := h.Hasher.Hash(raw)
	row, err := h.OAuth.LookupAccessTokenByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	if row.RevokedAt != nil {
		return nil, errAuth("token revoked")
	}
	if h.Now().After(row.ExpiresAt) {
		return nil, errAuth("token expired")
	}
	// Look up the user for username; for the hot path we'd cache, but the
	// users table is small and pgx's prepared statements amortise.
	user, err := h.Users.GetUserByID(ctx, row.UserID)
	if err != nil {
		return nil, err
	}
	go h.OAuth.TouchAccessToken(context.Background(), row.ID)

	return &auth.Identity{
		UserID:   row.UserID,
		Username: user.Username,
		Source:   auth.IdentitySourceOAT,
		Scopes:   row.Scopes,
		ClientID: row.ClientID,
	}, nil
}
