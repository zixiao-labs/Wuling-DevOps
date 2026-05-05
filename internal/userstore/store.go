// Package userstore is the persistence layer for users, orgs, projects, repos,
// and PATs. It holds plain SQL and never imports the HTTP layer.
//
// All slug lookups are case-insensitive (LOWER() index in the schema).
// All time values are returned in UTC.
package userstore

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
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// Store is the data-access object. All methods take a context and return
// either a value or an apperr-wrapped error.
type Store struct{ pool *db.Pool }

// New returns a Store backed by pool.
func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// ----------------------------------------------------------------------------
// Users
// ----------------------------------------------------------------------------

// CreateUserParams holds the inputs to CreateUser.
type CreateUserParams struct {
	Username     string
	Email        string
	DisplayName  string
	PasswordHash string // already argon2id-hashed
}

// CreateUser inserts a row, also creating the user's personal org and an
// owner membership in a single transaction.
func (s *Store) CreateUser(ctx context.Context, p CreateUserParams) (*model.User, *model.Org, error) {
	id := uuid.New()
	orgID := uuid.New()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	user := &model.User{
		ID:          id,
		Username:    p.Username,
		Email:       p.Email,
		DisplayName: defaultIfEmpty(p.DisplayName, p.Username),
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, username, email, display_name, password_hash)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING is_admin, is_active, created_at
	`, id, p.Username, p.Email, user.DisplayName, p.PasswordHash).
		Scan(&user.IsAdmin, &user.IsActive, &user.CreatedAt)
	if err != nil {
		return nil, nil, mapInsertErr(err, "user")
	}

	org := &model.Org{
		ID:          orgID,
		Slug:        strings.ToLower(p.Username),
		DisplayName: user.DisplayName,
		IsPersonal:  true,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO orgs (id, slug, display_name, is_personal, owner_user_id)
		VALUES ($1, $2, $3, TRUE, $4)
		RETURNING created_at
	`, org.ID, org.Slug, org.DisplayName, id).Scan(&org.CreatedAt)
	if err != nil {
		return nil, nil, mapInsertErr(err, "personal org")
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')
	`, org.ID, id); err != nil {
		return nil, nil, apperr.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, apperr.Internal(err)
	}
	return user, org, nil
}

// GetUserByLogin looks up a user by username OR email (case-insensitive).
// Returns the user plus their password hash so the auth handler can verify.
//
// When two users could plausibly match (e.g. user A whose username equals
// user B's email), we deterministically prefer the username match. Without
// the explicit ORDER BY the OR predicate would let Postgres return either
// row, depending on plan/index choice — that would surface as confusing
// "wrong account logged in" reports.
func (s *Store) GetUserByLogin(ctx context.Context, login string) (*model.User, string, error) {
	var u model.User
	var hash *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, email, display_name, is_admin, is_active, created_at, password_hash
		FROM users
		WHERE LOWER(username) = LOWER($1) OR LOWER(email) = LOWER($1)
		ORDER BY (LOWER(username) = LOWER($1)) DESC
		LIMIT 1
	`, login).Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", apperr.NotFound("user")
	}
	if err != nil {
		return nil, "", apperr.Internal(err)
	}
	if hash == nil {
		return nil, "", apperr.New(apperr.CodeUnauthorized, "user has no password set")
	}
	return &u, *hash, nil
}

// GetUserByID fetches a user by id.
func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, email, display_name, is_admin, is_active, created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin, &u.IsActive, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("user")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &u, nil
}

// GetUserByUsername fetches a user by username.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, email, display_name, is_admin, is_active, created_at
		FROM users WHERE LOWER(username) = LOWER($1)
	`, username).Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin, &u.IsActive, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("user")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &u, nil
}

// PasswordHashFor returns the hash for the username, used by the smart-HTTP
// password auth fallback.
func (s *Store) PasswordHashFor(ctx context.Context, username string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, password_hash FROM users WHERE LOWER(username) = LOWER($1) AND is_active
	`, username).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", apperr.NotFound("user")
	}
	if err != nil {
		return uuid.Nil, "", apperr.Internal(err)
	}
	if hash == nil {
		return uuid.Nil, "", apperr.Unauthorized("password auth not enabled for user")
	}
	return id, *hash, nil
}

// ----------------------------------------------------------------------------
// Orgs
// ----------------------------------------------------------------------------

// CreateOrgParams holds inputs to CreateOrg.
type CreateOrgParams struct {
	Slug        string
	DisplayName string
	Description string
	OwnerUserID uuid.UUID
}

// CreateOrg makes a non-personal org and grants the owner an 'owner' membership.
func (s *Store) CreateOrg(ctx context.Context, p CreateOrgParams) (*model.Org, error) {
	id := uuid.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	o := &model.Org{
		ID:          id,
		Slug:        strings.ToLower(p.Slug),
		DisplayName: defaultIfEmpty(p.DisplayName, p.Slug),
		Description: p.Description,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO orgs (id, slug, display_name, description, is_personal, owner_user_id)
		VALUES ($1, $2, $3, $4, FALSE, $5)
		RETURNING created_at
	`, o.ID, o.Slug, o.DisplayName, o.Description, p.OwnerUserID).Scan(&o.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "org")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')
	`, id, p.OwnerUserID); err != nil {
		return nil, apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	return o, nil
}

// GetOrgBySlug looks up an org by slug.
func (s *Store) GetOrgBySlug(ctx context.Context, slug string) (*model.Org, error) {
	var o model.Org
	err := s.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, description, is_personal, created_at
		FROM orgs WHERE LOWER(slug) = LOWER($1)
	`, slug).Scan(&o.ID, &o.Slug, &o.DisplayName, &o.Description, &o.IsPersonal, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("org")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &o, nil
}

// ListOrgsForUser returns orgs the user is a member of.
func (s *Store) ListOrgsForUser(ctx context.Context, userID uuid.UUID) ([]model.Org, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.display_name, o.description, o.is_personal, o.created_at
		FROM orgs o
		JOIN org_members m ON m.org_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.is_personal DESC, o.slug ASC
	`, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	var out []model.Org
	for rows.Next() {
		var o model.Org
		if err := rows.Scan(&o.ID, &o.Slug, &o.DisplayName, &o.Description, &o.IsPersonal, &o.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// MemberRole returns the user's role in the org, or "" if not a member.
func (s *Store) MemberRole(ctx context.Context, orgID, userID uuid.UUID) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", apperr.Internal(err)
	}
	return role, nil
}

// ----------------------------------------------------------------------------
// Projects
// ----------------------------------------------------------------------------

// CreateProjectParams holds inputs to CreateProject.
type CreateProjectParams struct {
	OrgID       uuid.UUID
	Slug        string
	DisplayName string
	Description string
	Visibility  string
}

// CreateProject inserts a project under an org.
func (s *Store) CreateProject(ctx context.Context, p CreateProjectParams) (*model.Project, error) {
	pj := &model.Project{
		ID:          uuid.New(),
		OrgID:       p.OrgID,
		Slug:        strings.ToLower(p.Slug),
		DisplayName: defaultIfEmpty(p.DisplayName, p.Slug),
		Description: p.Description,
		Visibility:  defaultIfEmpty(p.Visibility, "private"),
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (id, org_id, slug, display_name, description, visibility)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at
	`, pj.ID, pj.OrgID, pj.Slug, pj.DisplayName, pj.Description, pj.Visibility).Scan(&pj.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "project")
	}
	return pj, nil
}

// GetProjectBySlug returns a project under org slug.
func (s *Store) GetProjectBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*model.Project, error) {
	var p model.Project
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, slug, display_name, description, visibility, created_at
		FROM projects WHERE org_id = $1 AND LOWER(slug) = LOWER($2)
	`, orgID, slug).Scan(&p.ID, &p.OrgID, &p.Slug, &p.DisplayName, &p.Description, &p.Visibility, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("project")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &p, nil
}

// ListProjects returns all projects in an org.
func (s *Store) ListProjects(ctx context.Context, orgID uuid.UUID) ([]model.Project, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, slug, display_name, description, visibility, created_at
		FROM projects WHERE org_id = $1 ORDER BY slug ASC
	`, orgID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		var p model.Project
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Slug, &p.DisplayName, &p.Description, &p.Visibility, &p.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// Repos
// ----------------------------------------------------------------------------

// CreateRepoParams holds inputs to CreateRepo.
type CreateRepoParams struct {
	ProjectID     uuid.UUID
	Slug          string
	DisplayName   string
	Description   string
	DefaultBranch string
	Visibility    string
}

// CreateRepo persists a repo row. Caller is responsible for initialising the
// bare repository on disk afterwards.
func (s *Store) CreateRepo(ctx context.Context, p CreateRepoParams) (*model.Repo, error) {
	r := &model.Repo{
		ID:            uuid.New(),
		ProjectID:     p.ProjectID,
		Slug:          strings.ToLower(p.Slug),
		DisplayName:   defaultIfEmpty(p.DisplayName, p.Slug),
		Description:   p.Description,
		DefaultBranch: defaultIfEmpty(p.DefaultBranch, "main"),
		Visibility:    defaultIfEmpty(p.Visibility, "private"),
		IsEmpty:       true,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO repos (id, project_id, slug, display_name, description, default_branch, visibility)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at
	`, r.ID, r.ProjectID, r.Slug, r.DisplayName, r.Description, r.DefaultBranch, r.Visibility).Scan(&r.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "repo")
	}
	return r, nil
}

// GetRepoBySlug looks up a repo under a project.
func (s *Store) GetRepoBySlug(ctx context.Context, projectID uuid.UUID, slug string) (*model.Repo, error) {
	var r model.Repo
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, slug, display_name, description, default_branch, visibility, is_empty, size_bytes, created_at
		FROM repos WHERE project_id = $1 AND LOWER(slug) = LOWER($2)
	`, projectID, slug).Scan(&r.ID, &r.ProjectID, &r.Slug, &r.DisplayName, &r.Description, &r.DefaultBranch, &r.Visibility, &r.IsEmpty, &r.SizeBytes, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("repo")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &r, nil
}

// GetRepoByID looks up a repo by ID (used by smart HTTP after path resolution).
func (s *Store) GetRepoByID(ctx context.Context, id uuid.UUID) (*model.Repo, error) {
	var r model.Repo
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, slug, display_name, description, default_branch, visibility, is_empty, size_bytes, created_at
		FROM repos WHERE id = $1
	`, id).Scan(&r.ID, &r.ProjectID, &r.Slug, &r.DisplayName, &r.Description, &r.DefaultBranch, &r.Visibility, &r.IsEmpty, &r.SizeBytes, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("repo")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &r, nil
}

// ListRepos returns repos in a project.
func (s *Store) ListRepos(ctx context.Context, projectID uuid.UUID) ([]model.Repo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, slug, display_name, description, default_branch, visibility, is_empty, size_bytes, created_at
		FROM repos WHERE project_id = $1 ORDER BY slug ASC
	`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	var out []model.Repo
	for rows.Next() {
		var r model.Repo
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Slug, &r.DisplayName, &r.Description, &r.DefaultBranch, &r.Visibility, &r.IsEmpty, &r.SizeBytes, &r.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// MarkRepoNotEmpty flips is_empty=false. Called after first push.
func (s *Store) MarkRepoNotEmpty(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE repos SET is_empty = FALSE, updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// DeleteRepo removes a repo row by id. Used by the create path to roll back
// the metadata when bare-repo initialisation on disk fails, so we don't end
// up with orphaned DB rows pointing at non-existent on-disk repos.
func (s *Store) DeleteRepo(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, id)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("repo")
	}
	return nil
}

// ResolveRepoPath locates a repo by org/project/repo slugs and returns
// (repo, projectID, orgID).
func (s *Store) ResolveRepoPath(ctx context.Context, orgSlug, projectSlug, repoSlug string) (*model.Repo, uuid.UUID, uuid.UUID, error) {
	var r model.Repo
	var projectID, orgID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT r.id, r.project_id, r.slug, r.display_name, r.description,
		       r.default_branch, r.visibility, r.is_empty, r.size_bytes, r.created_at,
		       p.id, o.id
		FROM repos r
		JOIN projects p ON p.id = r.project_id
		JOIN orgs     o ON o.id = p.org_id
		WHERE LOWER(o.slug) = LOWER($1)
		  AND LOWER(p.slug) = LOWER($2)
		  AND LOWER(r.slug) = LOWER($3)
	`, orgSlug, projectSlug, repoSlug).Scan(
		&r.ID, &r.ProjectID, &r.Slug, &r.DisplayName, &r.Description,
		&r.DefaultBranch, &r.Visibility, &r.IsEmpty, &r.SizeBytes, &r.CreatedAt,
		&projectID, &orgID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uuid.Nil, uuid.Nil, apperr.NotFound("repo")
	}
	if err != nil {
		return nil, uuid.Nil, uuid.Nil, apperr.Internal(err)
	}
	return &r, projectID, orgID, nil
}

// ----------------------------------------------------------------------------
// PATs
// ----------------------------------------------------------------------------

// CreatePATParams holds inputs to CreatePAT.
type CreatePATParams struct {
	UserID    uuid.UUID
	Name      string
	Hash      string
	Scopes    []string
	ExpiresAt *time.Time
}

// CreatePAT inserts a personal access token row.
func (s *Store) CreatePAT(ctx context.Context, p CreatePATParams) (*model.AccessTokenView, error) {
	v := &model.AccessTokenView{
		ID:        uuid.New(),
		Name:      p.Name,
		Scopes:    p.Scopes,
		ExpiresAt: p.ExpiresAt,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO access_tokens (id, user_id, name, token_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at
	`, v.ID, p.UserID, p.Name, p.Hash, p.Scopes, p.ExpiresAt).Scan(&v.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "access token")
	}
	return v, nil
}

// ListPATsForUser returns all token metadata for the user.
func (s *Store) ListPATsForUser(ctx context.Context, userID uuid.UUID) ([]model.AccessTokenView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, scopes, expires_at, created_at
		FROM access_tokens WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	var out []model.AccessTokenView
	for rows.Next() {
		var v model.AccessTokenView
		if err := rows.Scan(&v.ID, &v.Name, &v.Scopes, &v.ExpiresAt, &v.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// ListPATHashesForUser returns (id, hash, scopes, expires_at) for matching all
// tokens belonging to a user. Used by smart-HTTP basic-auth fallback when the
// password field is a PAT — we have to argon2-compare against each row since
// argon2id salts differ per record.
type PATAuthRow struct {
	ID        uuid.UUID
	Hash      string
	Scopes    []string
	ExpiresAt *time.Time
}

// ListPATAuthRowsForUser returns rows for argon2 verification.
func (s *Store) ListPATAuthRowsForUser(ctx context.Context, userID uuid.UUID) ([]PATAuthRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, token_hash, scopes, expires_at
		FROM access_tokens WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	var out []PATAuthRow
	for rows.Next() {
		var r PATAuthRow
		if err := rows.Scan(&r.ID, &r.Hash, &r.Scopes, &r.ExpiresAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// TouchPAT updates last_used_at to now().
func (s *Store) TouchPAT(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE access_tokens SET last_used_at = now() WHERE id = $1`, id)
}

// DeletePAT removes a PAT belonging to the given user. Returns NotFound if
// the row does not exist (or belongs to another user, to avoid leaking ids).
func (s *Store) DeletePAT(ctx context.Context, userID, patID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM access_tokens WHERE id = $1 AND user_id = $2`, patID, userID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("access token")
	}
	return nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func defaultIfEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// mapInsertErr converts a pgconn unique-violation into a user-facing apperr.
func mapInsertErr(err error, kind string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return apperr.Conflict(fmt.Sprintf("%s already exists", kind))
		case "23503": // foreign_key_violation
			return apperr.New(apperr.CodeBadRequest, fmt.Sprintf("invalid reference creating %s", kind))
		case "23514": // check_violation
			return apperr.Validation(fmt.Sprintf("invalid value for %s", kind), nil)
		}
	}
	return apperr.Internal(err)
}
