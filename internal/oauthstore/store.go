// Package oauthstore is the persistence layer for the OAuth Authorization
// Server role. It owns six tables introduced in migration 0007_oauth:
//
//   - oauth_clients          : third-party / first-party app registrations
//   - oauth_authorizations   : per-(user, client) durable consent
//   - oauth_auth_requests    : server-side hold area for an /authorize call
//   - oauth_auth_codes       : short-lived authorization codes
//   - oauth_access_tokens    : live OATs + paired refresh tokens
//   - oauth_device_codes     : RFC 8628 device_code/user_code state
//   - oauth_audit_log        : append-only token lifecycle events
//
// All hashes (`*_hash` columns) are HMAC-SHA256 hex strings; the package never
// sees raw secrets — those are kept in the calling `oauthhttp` handlers and
// hashed before reaching us.
package oauthstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
)

// Store is the data-access object for OAuth provider state.
type Store struct{ pool *db.Pool }

// New returns a Store backed by pool.
func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// ----------------------------------------------------------------------------
// oauth_clients
// ----------------------------------------------------------------------------

// Client is the in-memory shape of an oauth_clients row. ClientSecretHash and
// OwnerUserID are pointers because they can be NULL (public clients and
// first-party clients respectively).
type Client struct {
	ID                 uuid.UUID
	ClientID           string
	ClientSecretHash   *string
	Name               string
	HomepageURL        string
	Description        string
	LogoURL            string
	OwnerUserID        *uuid.UUID
	IsFirstParty       bool
	IsConfidential     bool
	RedirectURIs       []string
	DefaultScopes      []string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CreateClientParams holds the inputs to CreateClient.
type CreateClientParams struct {
	ClientID         string
	ClientSecretHash *string
	Name             string
	HomepageURL      string
	Description      string
	LogoURL          string
	OwnerUserID      *uuid.UUID
	IsFirstParty     bool
	IsConfidential   bool
	RedirectURIs     []string
	DefaultScopes    []string
}

// CreateClient inserts a new oauth_clients row. Returns Conflict on duplicate
// client_id, BadRequest on bad references.
func (s *Store) CreateClient(ctx context.Context, p CreateClientParams) (*Client, error) {
	id := uuid.New()
	c := &Client{
		ID:               id,
		ClientID:         p.ClientID,
		ClientSecretHash: p.ClientSecretHash,
		Name:             p.Name,
		HomepageURL:      p.HomepageURL,
		Description:      p.Description,
		LogoURL:          p.LogoURL,
		OwnerUserID:      p.OwnerUserID,
		IsFirstParty:     p.IsFirstParty,
		IsConfidential:   p.IsConfidential,
		RedirectURIs:     p.RedirectURIs,
		DefaultScopes:    p.DefaultScopes,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_clients (id, client_id, client_secret_hash, name, homepage_url,
			description, logo_url, owner_user_id, is_first_party, is_confidential,
			redirect_uris, default_scopes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING created_at, updated_at
	`, id, p.ClientID, p.ClientSecretHash, p.Name, p.HomepageURL, p.Description,
		p.LogoURL, p.OwnerUserID, p.IsFirstParty, p.IsConfidential,
		p.RedirectURIs, p.DefaultScopes).Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "oauth client")
	}
	return c, nil
}

// UpsertFirstPartyClient guarantees a row exists with the given client_id and
// is_first_party=true. It is intended for server boot — the public client
// metadata may evolve (redirect URIs, scopes) but the client_id and identity
// stay constant across restarts.
//
// Returns the resulting row.
func (s *Store) UpsertFirstPartyClient(ctx context.Context, p CreateClientParams) (*Client, error) {
	id := uuid.New()
	c := &Client{
		ClientID:         p.ClientID,
		ClientSecretHash: nil,
		Name:             p.Name,
		HomepageURL:      p.HomepageURL,
		Description:      p.Description,
		LogoURL:          p.LogoURL,
		OwnerUserID:      nil,
		IsFirstParty:     true,
		IsConfidential:   false,
		RedirectURIs:     p.RedirectURIs,
		DefaultScopes:    p.DefaultScopes,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_clients (id, client_id, name, homepage_url, description, logo_url,
			is_first_party, is_confidential, redirect_uris, default_scopes)
		VALUES ($1,$2,$3,$4,$5,$6, TRUE, FALSE, $7, $8)
		ON CONFLICT (client_id) DO UPDATE SET
			name           = EXCLUDED.name,
			homepage_url   = EXCLUDED.homepage_url,
			description    = EXCLUDED.description,
			logo_url       = EXCLUDED.logo_url,
			redirect_uris  = EXCLUDED.redirect_uris,
			default_scopes = EXCLUDED.default_scopes,
			is_first_party = TRUE,
			is_confidential = FALSE,
			updated_at     = now()
		RETURNING id, created_at, updated_at
	`, id, p.ClientID, p.Name, p.HomepageURL, p.Description, p.LogoURL,
		p.RedirectURIs, p.DefaultScopes).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return c, nil
}

// GetClientByClientID returns the row identified by the public `client_id`
// string (NOT the internal UUID). NotFound when no row matches.
func (s *Store) GetClientByClientID(ctx context.Context, clientID string) (*Client, error) {
	return s.scanClient(ctx, `WHERE client_id = $1`, clientID)
}

// GetClientByID returns the row identified by internal UUID.
func (s *Store) GetClientByID(ctx context.Context, id uuid.UUID) (*Client, error) {
	return s.scanClient(ctx, `WHERE id = $1`, id)
}

// ListClientsForOwner returns the apps owned by a user.
func (s *Store) ListClientsForOwner(ctx context.Context, userID uuid.UUID) ([]Client, error) {
	rows, err := s.pool.Query(ctx, clientSelectSQL+` WHERE owner_user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return scanClients(rows)
}

// ListAllClients returns every registered client; admin-only.
func (s *Store) ListAllClients(ctx context.Context) ([]Client, error) {
	rows, err := s.pool.Query(ctx, clientSelectSQL+` ORDER BY is_first_party DESC, created_at DESC`)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return scanClients(rows)
}

// UpdateClient mutates a subset of fields. Pass nil to leave a field unchanged.
type UpdateClientParams struct {
	Name          *string
	HomepageURL   *string
	Description   *string
	LogoURL       *string
	RedirectURIs  *[]string
	DefaultScopes *[]string
	IsFirstParty  *bool
}

// UpdateClient mutates a client owned by ownerUserID. Pass nil ownerUserID to
// allow admin updates (skip ownership check).
func (s *Store) UpdateClient(ctx context.Context, id uuid.UUID, ownerUserID *uuid.UUID, p UpdateClientParams) error {
	sets := []string{"updated_at = now()"}
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.HomepageURL != nil {
		add("homepage_url", *p.HomepageURL)
	}
	if p.Description != nil {
		add("description", *p.Description)
	}
	if p.LogoURL != nil {
		add("logo_url", *p.LogoURL)
	}
	if p.RedirectURIs != nil {
		add("redirect_uris", *p.RedirectURIs)
	}
	if p.DefaultScopes != nil {
		add("default_scopes", *p.DefaultScopes)
	}
	if p.IsFirstParty != nil {
		add("is_first_party", *p.IsFirstParty)
	}
	where := "id = $1"
	if ownerUserID != nil {
		args = append(args, *ownerUserID)
		where = fmt.Sprintf("id = $1 AND owner_user_id = $%d", len(args))
	}
	q := fmt.Sprintf(`UPDATE oauth_clients SET %s WHERE %s`, strings.Join(sets, ", "), where)
	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("oauth client")
	}
	return nil
}

// RotateClientSecret writes a new client_secret_hash on a confidential client.
// Returns NotFound if the row doesn't match ownerUserID (pass nil for admin).
func (s *Store) RotateClientSecret(ctx context.Context, id uuid.UUID, ownerUserID *uuid.UUID, newSecretHash string) error {
	where := "id = $1"
	args := []any{id, newSecretHash}
	if ownerUserID != nil {
		args = append(args, *ownerUserID)
		where = "id = $1 AND owner_user_id = $3"
	}
	q := fmt.Sprintf(`UPDATE oauth_clients SET client_secret_hash = $2, updated_at = now() WHERE %s AND is_confidential = TRUE`, where)
	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("oauth client")
	}
	return nil
}

// DeleteClient removes a client owned by ownerUserID (or any client if nil).
func (s *Store) DeleteClient(ctx context.Context, id uuid.UUID, ownerUserID *uuid.UUID) error {
	where := "id = $1"
	args := []any{id}
	if ownerUserID != nil {
		args = append(args, *ownerUserID)
		where = "id = $1 AND owner_user_id = $2"
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM oauth_clients WHERE `+where, args...)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("oauth client")
	}
	return nil
}

const clientSelectSQL = `
	SELECT id, client_id, client_secret_hash, name, homepage_url, description, logo_url,
	       owner_user_id, is_first_party, is_confidential, redirect_uris, default_scopes,
	       created_at, updated_at
	FROM oauth_clients
`

func (s *Store) scanClient(ctx context.Context, where string, args ...any) (*Client, error) {
	c := &Client{}
	err := s.pool.QueryRow(ctx, clientSelectSQL+where, args...).Scan(
		&c.ID, &c.ClientID, &c.ClientSecretHash, &c.Name, &c.HomepageURL,
		&c.Description, &c.LogoURL, &c.OwnerUserID, &c.IsFirstParty,
		&c.IsConfidential, &c.RedirectURIs, &c.DefaultScopes,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("oauth client")
		}
		return nil, apperr.Internal(err)
	}
	return c, nil
}

func scanClients(rows pgx.Rows) ([]Client, error) {
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var c Client
		if err := rows.Scan(
			&c.ID, &c.ClientID, &c.ClientSecretHash, &c.Name, &c.HomepageURL,
			&c.Description, &c.LogoURL, &c.OwnerUserID, &c.IsFirstParty,
			&c.IsConfidential, &c.RedirectURIs, &c.DefaultScopes,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// oauth_authorizations
// ----------------------------------------------------------------------------

// Authorization is a durable user-to-app consent record.
type Authorization struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ClientID  uuid.UUID
	Scopes    []string
	GrantedAt time.Time
	UpdatedAt time.Time
}

// AuthorizationView is Authorization joined with the client metadata that the
// user UI wants to render (name, logo, owner_login). Filled in by the handler
// via a JOIN — kept in the store for proximity to the SQL.
type AuthorizationView struct {
	Authorization
	ClientPublicID string
	ClientName     string
	ClientLogoURL  string
	IsFirstParty   bool
}

// UpsertAuthorization stores or replaces (user, client) consent. Scopes are
// the post-decision set — overwrite, never append, so revoked-by-the-user
// scopes are not silently re-granted on a future request.
func (s *Store) UpsertAuthorization(ctx context.Context, userID, clientID uuid.UUID, scopes []string) (*Authorization, error) {
	id := uuid.New()
	a := &Authorization{
		UserID:   userID,
		ClientID: clientID,
		Scopes:   scopes,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_authorizations (id, user_id, client_id, scopes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, client_id) DO UPDATE SET
			scopes     = EXCLUDED.scopes,
			updated_at = now()
		RETURNING id, granted_at, updated_at
	`, id, userID, clientID, scopes).Scan(&a.ID, &a.GrantedAt, &a.UpdatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "authorization")
	}
	return a, nil
}

// GetAuthorization returns the (user, client) consent row if any. NotFound
// when the user has never consented to this client.
func (s *Store) GetAuthorization(ctx context.Context, userID, clientID uuid.UUID) (*Authorization, error) {
	a := &Authorization{UserID: userID, ClientID: clientID}
	err := s.pool.QueryRow(ctx, `
		SELECT id, scopes, granted_at, updated_at
		FROM oauth_authorizations
		WHERE user_id = $1 AND client_id = $2
	`, userID, clientID).Scan(&a.ID, &a.Scopes, &a.GrantedAt, &a.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("authorization")
		}
		return nil, apperr.Internal(err)
	}
	return a, nil
}

// ListAuthorizationsForUser returns every (client) the user has granted,
// joined with the public client metadata for the consent UI.
func (s *Store) ListAuthorizationsForUser(ctx context.Context, userID uuid.UUID) ([]AuthorizationView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.user_id, a.client_id, a.scopes, a.granted_at, a.updated_at,
		       c.client_id, c.name, c.logo_url, c.is_first_party
		FROM oauth_authorizations a
		JOIN oauth_clients c ON c.id = a.client_id
		WHERE a.user_id = $1
		ORDER BY a.updated_at DESC
	`, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	var out []AuthorizationView
	for rows.Next() {
		var v AuthorizationView
		if err := rows.Scan(&v.ID, &v.UserID, &v.ClientID, &v.Scopes,
			&v.GrantedAt, &v.UpdatedAt,
			&v.ClientPublicID, &v.ClientName, &v.ClientLogoURL, &v.IsFirstParty); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevokeAuthorization deletes the consent row AND every live access token
// minted under it. Used by `DELETE /authorizations/{id}`.
func (s *Store) RevokeAuthorization(ctx context.Context, userID, authorizationID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var clientID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT client_id FROM oauth_authorizations
		WHERE id = $1 AND user_id = $2
	`, authorizationID, userID).Scan(&clientID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound("authorization")
		}
		return apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM oauth_authorizations WHERE id = $1`, authorizationID); err != nil {
		return apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE oauth_access_tokens SET revoked_at = now()
		WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL
	`, userID, clientID); err != nil {
		return apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// oauth_auth_requests
// ----------------------------------------------------------------------------

// AuthRequest mirrors the server-side hold area for an /authorize call.
type AuthRequest struct {
	ID                  uuid.UUID
	ClientID            uuid.UUID
	RedirectURI         string
	Scopes              []string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	SessionCookieHash   string
	UserID              *uuid.UUID
	Decision            *string
	ExpiresAt           time.Time
	CreatedAt           time.Time
}

// CreateAuthRequestParams holds the inputs to CreateAuthRequest.
type CreateAuthRequestParams struct {
	ClientID            uuid.UUID
	RedirectURI         string
	Scopes              []string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	SessionCookieHash   string
	TTL                 time.Duration
}

// CreateAuthRequest inserts a row and returns the id. Callers stash that id in
// the URL hand-off to the frontend.
func (s *Store) CreateAuthRequest(ctx context.Context, p CreateAuthRequestParams) (*AuthRequest, error) {
	id := uuid.New()
	expires := time.Now().Add(p.TTL)
	a := &AuthRequest{
		ID:                  id,
		ClientID:            p.ClientID,
		RedirectURI:         p.RedirectURI,
		Scopes:              p.Scopes,
		State:               p.State,
		CodeChallenge:       p.CodeChallenge,
		CodeChallengeMethod: p.CodeChallengeMethod,
		SessionCookieHash:   p.SessionCookieHash,
		ExpiresAt:           expires,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_auth_requests
			(id, client_id, redirect_uri, scopes, state, code_challenge,
			 code_challenge_method, session_cookie_hash, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING created_at
	`, id, p.ClientID, p.RedirectURI, p.Scopes, p.State, p.CodeChallenge,
		p.CodeChallengeMethod, p.SessionCookieHash, expires).Scan(&a.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "auth request")
	}
	return a, nil
}

// GetAuthRequest returns the row by id. NotFound when expired or unknown.
func (s *Store) GetAuthRequest(ctx context.Context, id uuid.UUID) (*AuthRequest, error) {
	a := &AuthRequest{ID: id}
	err := s.pool.QueryRow(ctx, `
		SELECT client_id, redirect_uri, scopes, state, code_challenge,
		       code_challenge_method, session_cookie_hash, user_id, decision,
		       expires_at, created_at
		FROM oauth_auth_requests
		WHERE id = $1
	`, id).Scan(&a.ClientID, &a.RedirectURI, &a.Scopes, &a.State, &a.CodeChallenge,
		&a.CodeChallengeMethod, &a.SessionCookieHash, &a.UserID, &a.Decision,
		&a.ExpiresAt, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("auth request")
		}
		return nil, apperr.Internal(err)
	}
	if time.Now().After(a.ExpiresAt) {
		return nil, apperr.NotFound("auth request expired")
	}
	return a, nil
}

// RecordAuthRequestDecision atomically writes user_id + decision and returns
// the resulting row. Returns Conflict if a decision is already recorded.
func (s *Store) RecordAuthRequestDecision(ctx context.Context, id, userID uuid.UUID, decision string) (*AuthRequest, error) {
	a := &AuthRequest{ID: id, UserID: &userID}
	err := s.pool.QueryRow(ctx, `
		UPDATE oauth_auth_requests
		   SET user_id  = $2,
		       decision = $3
		 WHERE id = $1
		   AND decision IS NULL
		   AND expires_at > now()
		RETURNING client_id, redirect_uri, scopes, state, code_challenge,
		          code_challenge_method, session_cookie_hash, decision,
		          expires_at, created_at
	`, id, userID, decision).Scan(&a.ClientID, &a.RedirectURI, &a.Scopes, &a.State,
		&a.CodeChallenge, &a.CodeChallengeMethod, &a.SessionCookieHash,
		&a.Decision, &a.ExpiresAt, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.New(apperr.CodeConflict, "auth request already decided or expired")
		}
		return nil, apperr.Internal(err)
	}
	return a, nil
}

// DeleteExpiredAuthRequests is a sweep, called by a background job.
func (s *Store) DeleteExpiredAuthRequests(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM oauth_auth_requests WHERE expires_at < now()`)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	return tag.RowsAffected(), nil
}

// ----------------------------------------------------------------------------
// oauth_auth_codes
// ----------------------------------------------------------------------------

// CreateAuthCodeParams collects the inputs to mint an authorization code row.
type CreateAuthCodeParams struct {
	CodeHash      string
	ClientID      uuid.UUID
	UserID        uuid.UUID
	RedirectURI   string
	Scopes        []string
	CodeChallenge string
	TTL           time.Duration
}

// CreateAuthCode inserts a row keyed by the code hash. The raw code is held
// by the caller, who returns it in the redirect URL.
func (s *Store) CreateAuthCode(ctx context.Context, p CreateAuthCodeParams) error {
	expires := time.Now().Add(p.TTL)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_auth_codes
			(code_hash, client_id, user_id, redirect_uri, scopes, code_challenge, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, p.CodeHash, p.ClientID, p.UserID, p.RedirectURI, p.Scopes, p.CodeChallenge, expires)
	if err != nil {
		return mapInsertErr(err, "auth code")
	}
	return nil
}

// AuthCodeRow is the consumable shape returned by ConsumeAuthCode.
type AuthCodeRow struct {
	ClientID      uuid.UUID
	UserID        uuid.UUID
	RedirectURI   string
	Scopes        []string
	CodeChallenge string
}

// ConsumeAuthCode looks up the row by code_hash, marks it used in the same
// transaction, and returns its contents. Returns Conflict if the row was
// already consumed or has expired — both cases should trigger invalid_grant
// at the caller.
func (s *Store) ConsumeAuthCode(ctx context.Context, codeHash string) (*AuthCodeRow, error) {
	r := &AuthCodeRow{}
	err := s.pool.QueryRow(ctx, `
		UPDATE oauth_auth_codes
		   SET used_at = now()
		 WHERE code_hash = $1
		   AND used_at IS NULL
		   AND expires_at > now()
		RETURNING client_id, user_id, redirect_uri, scopes, code_challenge
	`, codeHash).Scan(&r.ClientID, &r.UserID, &r.RedirectURI, &r.Scopes, &r.CodeChallenge)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.New(apperr.CodeConflict, "auth code already used or expired")
		}
		return nil, apperr.Internal(err)
	}
	return r, nil
}

// ----------------------------------------------------------------------------
// oauth_access_tokens
// ----------------------------------------------------------------------------

// AccessTokenRow mirrors a row in oauth_access_tokens (subset useful at runtime).
type AccessTokenRow struct {
	ID                uuid.UUID
	UserID            uuid.UUID
	ClientID          uuid.UUID
	Scopes            []string
	ExpiresAt         time.Time
	RefreshExpiresAt  *time.Time
	RefreshChainID    *uuid.UUID
	ParentRefreshHash *string
	RevokedAt         *time.Time
	CreatedAt         time.Time
	LastUsedAt        *time.Time
}

// CreateAccessTokenParams collects the inputs to mint an access+refresh row.
type CreateAccessTokenParams struct {
	UserID            uuid.UUID
	ClientID          uuid.UUID
	TokenHash         string
	Scopes            []string
	TokenTTL          time.Duration
	RefreshTokenHash  *string // nil = no refresh
	RefreshTTL        time.Duration
	RefreshChainID    *uuid.UUID
	ParentRefreshHash *string
}

// CreateAccessToken inserts a new live token row and returns its id.
func (s *Store) CreateAccessToken(ctx context.Context, p CreateAccessTokenParams) (*AccessTokenRow, error) {
	id := uuid.New()
	now := time.Now()
	expires := now.Add(p.TokenTTL)
	var refreshExpires *time.Time
	if p.RefreshTokenHash != nil && p.RefreshTTL > 0 {
		t := now.Add(p.RefreshTTL)
		refreshExpires = &t
	}
	chainID := p.RefreshChainID
	if chainID == nil && p.RefreshTokenHash != nil {
		// Root of a new chain.
		nc := uuid.New()
		chainID = &nc
	}
	a := &AccessTokenRow{
		ID:               id,
		UserID:           p.UserID,
		ClientID:         p.ClientID,
		Scopes:           p.Scopes,
		ExpiresAt:        expires,
		RefreshExpiresAt: refreshExpires,
		RefreshChainID:   chainID,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_access_tokens
			(id, user_id, client_id, token_hash, scopes, expires_at,
			 refresh_token_hash, refresh_expires_at, refresh_chain_id, parent_refresh_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING created_at
	`, id, p.UserID, p.ClientID, p.TokenHash, p.Scopes, expires,
		p.RefreshTokenHash, refreshExpires, chainID, p.ParentRefreshHash).Scan(&a.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "access token")
	}
	return a, nil
}

// LookupAccessTokenByHash returns the live token (or NotFound). Caller is
// responsible for checking RevokedAt / ExpiresAt before honouring the bearer.
func (s *Store) LookupAccessTokenByHash(ctx context.Context, tokenHash string) (*AccessTokenRow, error) {
	a := &AccessTokenRow{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, client_id, scopes, expires_at,
		       refresh_expires_at, refresh_chain_id, parent_refresh_hash,
		       revoked_at, created_at, last_used_at
		FROM oauth_access_tokens
		WHERE token_hash = $1
	`, tokenHash).Scan(&a.ID, &a.UserID, &a.ClientID, &a.Scopes, &a.ExpiresAt,
		&a.RefreshExpiresAt, &a.RefreshChainID, &a.ParentRefreshHash,
		&a.RevokedAt, &a.CreatedAt, &a.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("access token")
		}
		return nil, apperr.Internal(err)
	}
	return a, nil
}

// LookupAccessTokenByRefreshHash is the same lookup keyed on refresh_token_hash.
func (s *Store) LookupAccessTokenByRefreshHash(ctx context.Context, refreshHash string) (*AccessTokenRow, error) {
	a := &AccessTokenRow{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, client_id, scopes, expires_at,
		       refresh_expires_at, refresh_chain_id, parent_refresh_hash,
		       revoked_at, created_at, last_used_at
		FROM oauth_access_tokens
		WHERE refresh_token_hash = $1
	`, refreshHash).Scan(&a.ID, &a.UserID, &a.ClientID, &a.Scopes, &a.ExpiresAt,
		&a.RefreshExpiresAt, &a.RefreshChainID, &a.ParentRefreshHash,
		&a.RevokedAt, &a.CreatedAt, &a.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("refresh token")
		}
		return nil, apperr.Internal(err)
	}
	return a, nil
}

// RevokeAccessToken marks a single token row revoked. No-op when already revoked.
func (s *Store) RevokeAccessToken(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_access_tokens SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL
	`, id)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// RevokeChain revokes every live row in a refresh chain — used on refresh
// reuse detection (RFC 6819 §5.2.2.3).
func (s *Store) RevokeChain(ctx context.Context, chainID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_access_tokens SET revoked_at = now()
		WHERE refresh_chain_id = $1 AND revoked_at IS NULL
	`, chainID)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// TouchAccessToken updates last_used_at; fire-and-forget on the hot read path.
func (s *Store) TouchAccessToken(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE oauth_access_tokens SET last_used_at = now() WHERE id = $1`, id)
}

// ----------------------------------------------------------------------------
// oauth_device_codes
// ----------------------------------------------------------------------------

// DeviceCodeRow is a row in oauth_device_codes.
type DeviceCodeRow struct {
	DeviceCodeHash string
	UserCode       string
	ClientID       uuid.UUID
	Scopes         []string
	UserID         *uuid.UUID
	Status         string
	IntervalSec    int
	LastPolledAt   *time.Time
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// CreateDeviceCodeParams collects the inputs to mint a device flow row.
type CreateDeviceCodeParams struct {
	DeviceCodeHash string
	UserCode       string
	ClientID       uuid.UUID
	Scopes         []string
	IntervalSec    int
	TTL            time.Duration
}

// CreateDeviceCode inserts a fresh pending row.
func (s *Store) CreateDeviceCode(ctx context.Context, p CreateDeviceCodeParams) (*DeviceCodeRow, error) {
	expires := time.Now().Add(p.TTL)
	r := &DeviceCodeRow{
		DeviceCodeHash: p.DeviceCodeHash,
		UserCode:       p.UserCode,
		ClientID:       p.ClientID,
		Scopes:         p.Scopes,
		Status:         "pending",
		IntervalSec:    p.IntervalSec,
		ExpiresAt:      expires,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_device_codes
			(device_code_hash, user_code, client_id, scopes, interval_sec, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING created_at
	`, p.DeviceCodeHash, p.UserCode, p.ClientID, p.Scopes, p.IntervalSec, expires).Scan(&r.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "device code")
	}
	return r, nil
}

// GetDeviceCodeByHash returns the row that the device is polling on.
func (s *Store) GetDeviceCodeByHash(ctx context.Context, deviceCodeHash string) (*DeviceCodeRow, error) {
	return s.scanDeviceCode(ctx, `WHERE device_code_hash = $1`, deviceCodeHash)
}

// GetDeviceCodeByUserCode returns the row the user typed into the browser.
func (s *Store) GetDeviceCodeByUserCode(ctx context.Context, userCode string) (*DeviceCodeRow, error) {
	return s.scanDeviceCode(ctx, `WHERE user_code = $1`, userCode)
}

// ApproveDeviceCode flips status pending -> approved and attaches the user.
func (s *Store) ApproveDeviceCode(ctx context.Context, userCode string, userID uuid.UUID, scopes []string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE oauth_device_codes
		   SET status = 'approved',
		       user_id = $2,
		       scopes  = $3
		 WHERE user_code = $1
		   AND status    = 'pending'
		   AND expires_at > now()
	`, userCode, userID, scopes)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("device code")
	}
	return nil
}

// DenyDeviceCode marks status denied.
func (s *Store) DenyDeviceCode(ctx context.Context, userCode string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_device_codes SET status = 'denied'
		WHERE user_code = $1 AND status = 'pending'
	`, userCode)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// TouchDevicePoll records a poll attempt; used for `slow_down` detection in
// the device-flow grant handler.
func (s *Store) TouchDevicePoll(ctx context.Context, deviceCodeHash string) {
	_, _ = s.pool.Exec(ctx, `
		UPDATE oauth_device_codes SET last_polled_at = now()
		WHERE device_code_hash = $1
	`, deviceCodeHash)
}

func (s *Store) scanDeviceCode(ctx context.Context, where string, args ...any) (*DeviceCodeRow, error) {
	r := &DeviceCodeRow{}
	err := s.pool.QueryRow(ctx, `
		SELECT device_code_hash, user_code, client_id, scopes, user_id,
		       status, interval_sec, last_polled_at, expires_at, created_at
		FROM oauth_device_codes
	`+where, args...).Scan(&r.DeviceCodeHash, &r.UserCode, &r.ClientID, &r.Scopes,
		&r.UserID, &r.Status, &r.IntervalSec, &r.LastPolledAt, &r.ExpiresAt, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("device code")
		}
		return nil, apperr.Internal(err)
	}
	return r, nil
}

// ----------------------------------------------------------------------------
// oauth_audit_log
// ----------------------------------------------------------------------------

// Audit appends an event to oauth_audit_log. Fire-and-forget — auditing is
// best-effort; we don't fail user requests because the log write blipped.
func (s *Store) Audit(ctx context.Context, event string, userID, clientID *uuid.UUID, meta map[string]any) {
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO oauth_audit_log (user_id, client_id, event, meta)
		VALUES ($1, $2, $3, $4)
	`, userID, clientID, event, meta)
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func mapInsertErr(err error, kind string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return apperr.Conflict(fmt.Sprintf("%s already exists", kind))
		case "23503":
			return apperr.New(apperr.CodeBadRequest, fmt.Sprintf("invalid reference creating %s", kind))
		case "23514":
			return apperr.Validation(fmt.Sprintf("invalid value for %s", kind), nil)
		}
	}
	return apperr.Internal(err)
}
