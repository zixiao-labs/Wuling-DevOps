package githubwebhook

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/db"
)

// RepoLink binds a GitHub repository to a Wuling repo.
type RepoLink struct {
	ID             uuid.UUID `json:"id"`
	InstallationID int64     `json:"installation_id"`
	Owner          string    `json:"owner"`
	Name           string    `json:"name"`
	OrgID          uuid.UUID `json:"org_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	RepoID         uuid.UUID `json:"repo_id"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// LinkStore persists github_repo_links.
type LinkStore struct {
	Pool *db.Pool
}

// GetByFullName returns the active link for owner/name (case-insensitive).
func (s *LinkStore) GetByFullName(ctx context.Context, owner, name string) (*RepoLink, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, owner, name, org_id, project_id, repo_id, active, created_at, updated_at
		FROM github_repo_links
		WHERE active AND LOWER(owner) = LOWER($1) AND LOWER(name) = LOWER($2)
	`, owner, name)
	return scanLink(row)
}

// GetByRepoID returns the link row for a Wuling repo (active or not).
func (s *LinkStore) GetByRepoID(ctx context.Context, repoID uuid.UUID) (*RepoLink, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, owner, name, org_id, project_id, repo_id, active, created_at, updated_at
		FROM github_repo_links
		WHERE repo_id = $1
	`, repoID)
	return scanLink(row)
}

// Upsert creates or updates the link for a Wuling repo.
func (s *LinkStore) Upsert(ctx context.Context, p RepoLink) (*RepoLink, error) {
	p.Owner = strings.TrimSpace(p.Owner)
	p.Name = strings.TrimSpace(p.Name)
	if p.Owner == "" || p.Name == "" {
		return nil, errors.New("owner and name are required")
	}
	if p.ID == uuid.Nil {
		p.ID = uuid.Must(uuid.NewV7())
	}
	// Free the full_name unique slot if another active row holds it.
	_, _ = s.Pool.Exec(ctx, `
		UPDATE github_repo_links SET active = FALSE, updated_at = now()
		WHERE active AND LOWER(owner) = LOWER($1) AND LOWER(name) = LOWER($2) AND repo_id <> $3
	`, p.Owner, p.Name, p.RepoID)

	_, err := s.Pool.Exec(ctx, `
		INSERT INTO github_repo_links (
			id, installation_id, owner, name, org_id, project_id, repo_id, active
		) VALUES ($1,$2,$3,$4,$5,$6,$7,TRUE)
		ON CONFLICT (repo_id) DO UPDATE SET
			installation_id = EXCLUDED.installation_id,
			owner = EXCLUDED.owner,
			name = EXCLUDED.name,
			org_id = EXCLUDED.org_id,
			project_id = EXCLUDED.project_id,
			active = TRUE,
			updated_at = now()
	`, p.ID, p.InstallationID, p.Owner, p.Name, p.OrgID, p.ProjectID, p.RepoID)
	if err != nil {
		return nil, err
	}
	return s.GetByRepoID(ctx, p.RepoID)
}

// DeactivateByInstallation marks all links for an installation inactive.
func (s *LinkStore) DeactivateByInstallation(ctx context.Context, installationID int64) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE github_repo_links SET active = FALSE, updated_at = now()
		WHERE installation_id = $1 AND active
	`, installationID)
	return err
}

// DeactivateRepo marks links for owner/name inactive.
func (s *LinkStore) DeactivateRepo(ctx context.Context, owner, name string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE github_repo_links SET active = FALSE, updated_at = now()
		WHERE active AND LOWER(owner) = LOWER($1) AND LOWER(name) = LOWER($2)
	`, owner, name)
	return err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanLink(row scannable) (*RepoLink, error) {
	var l RepoLink
	err := row.Scan(
		&l.ID, &l.InstallationID, &l.Owner, &l.Name,
		&l.OrgID, &l.ProjectID, &l.RepoID, &l.Active,
		&l.CreatedAt, &l.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}
