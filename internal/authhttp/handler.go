// Package authhttp wires HTTP handlers for register/login/me + PAT management.
//
// All handlers respond with the canonical {"error":{"code","message"}} envelope
// on failure (via httpapi.RenderError) and with a JSON DTO on success.
package authhttp

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler bundles dependencies for the auth HTTP routes.
type Handler struct {
	Store    *userstore.Store
	Issuer   *auth.Issuer
	Verifier *auth.Verifier
}

// Mount registers the routes on r. The base path is conventionally "/api/v1/auth".
func (h *Handler) Mount(r chi.Router) {
	r.Post("/register", h.register)
	r.Post("/login", h.login)
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))
		r.Get("/me", h.me)
		r.Get("/tokens", h.listTokens)
		r.Post("/tokens", h.createToken)
		r.Delete("/tokens/{token_id}", h.deleteToken)
	})
}

// ---------- request/response shapes ----------

type registerReq struct {
	Username    string `json:"username"     validate:"required,min=2,max=64,alphanumdash"`
	Email       string `json:"email"        validate:"required,email,max=320"`
	Password    string `json:"password"     validate:"required,min=8,max=256"`
	DisplayName string `json:"display_name" validate:"max=128"`
}

type loginReq struct {
	Login    string `json:"login"    validate:"required,min=2,max=320"`
	Password string `json:"password" validate:"required,min=1,max=256"`
}

type tokenResp struct {
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"`
	ExpiresAt   time.Time   `json:"expires_at"`
	User        *model.User `json:"user"`
}

type createTokenReq struct {
	Name      string     `json:"name"       validate:"required,min=1,max=64"`
	Scopes    []string   `json:"scopes"     validate:"omitempty,dive,oneof=repo:read repo:write"`
	ExpiresAt *time.Time `json:"expires_at" validate:"omitempty"`
}

func init() {
	// "alphanumdash" is "letters, digits, _ and -"; we register it as a custom
	// validator. Username must start with a letter for path-routing sanity.
	_ = httpapi.Validator.RegisterValidation("alphanumdash", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if len(s) == 0 {
			return false
		}
		first := s[0]
		if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
			return false
		}
		for i := 1; i < len(s); i++ {
			c := s[i]
			ok := (c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') ||
				c == '_' || c == '-'
			if !ok {
				return false
			}
		}
		return true
	})
}

// ---------- handlers ----------

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	user, _, err := h.Store.CreateUser(r.Context(), userstore.CreateUserParams{
		Username:     strings.TrimSpace(req.Username),
		Email:        strings.ToLower(strings.TrimSpace(req.Email)),
		DisplayName:  req.DisplayName,
		PasswordHash: hash,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	tok, exp, err := h.Issuer.Issue(user.ID, user.Username)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, tokenResp{
		AccessToken: tok,
		TokenType:   "Bearer",
		ExpiresAt:   exp,
		User:        user,
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	user, hash, err := h.Store.GetUserByLogin(r.Context(), req.Login)
	if err != nil {
		// Don't reveal whether the user exists — collapse not_found to unauth.
		if e := apperr.As(err); e != nil && e.Code == apperr.CodeNotFound {
			httpapi.RenderError(w, r, apperr.Unauthorized("invalid credentials"))
			return
		}
		httpapi.RenderError(w, r, err)
		return
	}
	ok, verr := auth.VerifyPassword(req.Password, hash)
	if verr != nil {
		httpapi.RenderError(w, r, apperr.Internal(verr))
		return
	}
	if !ok {
		httpapi.RenderError(w, r, apperr.Unauthorized("invalid credentials"))
		return
	}
	if !user.IsActive {
		httpapi.RenderError(w, r, apperr.Forbidden("account is disabled"))
		return
	}
	tok, exp, err := h.Issuer.Issue(user.ID, user.Username)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, tokenResp{
		AccessToken: tok,
		TokenType:   "Bearer",
		ExpiresAt:   exp,
		User:        user,
	})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	u, err := h.Store.GetUserByID(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, u)
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createTokenReq
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"repo:read", "repo:write"}
	}
	raw, hashed, err := auth.NewAccessToken()
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	view, err := h.Store.CreatePAT(r.Context(), userstore.CreatePATParams{
		UserID:    id.UserID,
		Name:      req.Name,
		Hash:      hashed,
		Scopes:    req.Scopes,
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	view.Token = raw
	httpapi.WriteJSON(w, http.StatusCreated, view)
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	views, err := h.Store.ListPATsForUser(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"tokens": views})
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	tokIDStr := chi.URLParam(r, "token_id")
	tokID, perr := uuid.Parse(tokIDStr)
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid token id"))
		return
	}
	if err := h.Store.DeletePAT(r.Context(), id.UserID, tokID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- resolvers used by smart-HTTP basic auth ----------

// PasswordResolver implements auth.PasswordResolver. We support the password
// fallback so that `git clone https://user:pass@host` works for users who
// haven't issued a PAT yet — at the cost of slightly looser security than
// PAT-only mode. Operators can disable it once tooling is in place.
type PasswordResolver struct{ Store *userstore.Store }

// ResolvePassword authenticates user/password against the users table.
func (p *PasswordResolver) ResolvePassword(ctx context.Context, username, password string) (*auth.Identity, error) {
	id, hash, err := p.Store.PasswordHashFor(ctx, username)
	if err != nil {
		return nil, err
	}
	ok, verr := auth.VerifyPassword(password, hash)
	if verr != nil {
		return nil, apperr.Internal(verr)
	}
	if !ok {
		return nil, apperr.Unauthorized("invalid credentials")
	}
	return &auth.Identity{
		UserID:   id,
		Username: username,
		Source:   auth.IdentitySourceJWT, // password-derived JWT-equivalent
	}, nil
}

// PATResolver implements auth.PATResolver.
type PATResolver struct{ Store *userstore.Store }

// ResolvePAT authenticates a username + raw PAT secret. It pulls all PATs for
// the user and argon2-compares each (PATs are typically <10 per user, and the
// argon2 cost is the dominant factor here regardless).
func (p *PATResolver) ResolvePAT(ctx context.Context, username, raw string) (*auth.Identity, error) {
	if !strings.HasPrefix(raw, auth.AccessTokenPrefix) {
		return nil, apperr.Unauthorized("invalid token")
	}
	user, err := p.Store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	rows, err := p.Store.ListPATAuthRowsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.ExpiresAt != nil && row.ExpiresAt.Before(now) {
			continue
		}
		ok, verr := auth.VerifyAccessToken(raw, row.Hash)
		if verr != nil {
			continue
		}
		if !ok {
			continue
		}
		p.Store.TouchPAT(ctx, row.ID)
		return &auth.Identity{
			UserID:   user.ID,
			Username: user.Username,
			Source:   auth.IdentitySourcePAT,
			Scopes:   row.Scopes,
		}, nil
	}
	return nil, apperr.Unauthorized("invalid token")
}
