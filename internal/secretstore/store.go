// Package secretstore is the persistence layer for Secrets: org- and
// project-scoped encrypted values. Like the other stores it never imports the
// HTTP layer and returns apperr-wrapped errors. Plaintext leaves this package
// only through the explicit Resolve*/GetValue methods — the List/model shapes
// are metadata-only.
package secretstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/secretbox"
)

// nameRe mirrors the CHECK constraint in 0009_pipelines.up.sql: an
// env-var-safe identifier, so a secret name can be injected as an environment
// variable verbatim.
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// MaxValueBytes caps a single secret's plaintext size. Generous enough for
// cloud credential JSON / private keys, small enough to keep a malicious
// payload from ballooning a row.
const MaxValueBytes = 64 * 1024

// Store is the data-access object for Secrets.
type Store struct {
	pool *db.Pool
	box  *secretbox.Box
}

// New returns a Store backed by pool, encrypting with box.
func New(pool *db.Pool, box *secretbox.Box) *Store {
	return &Store{pool: pool, box: box}
}

// SetParams holds inputs to Set. ProjectID nil = org scope.
type SetParams struct {
	OrgID     uuid.UUID
	ProjectID *uuid.UUID
	Name      string
	Value     string
	CreatedBy uuid.UUID
}

// Set creates or replaces a secret by (scope, name). Encryption happens here;
// only ciphertext + nonce reach the DB.
func (s *Store) Set(ctx context.Context, p SetParams) (*model.Secret, error) {
	name := strings.TrimSpace(p.Name)
	if !nameRe.MatchString(name) {
		return nil, apperr.Validation("secret name must match ^[A-Za-z_][A-Za-z0-9_]*$", nil)
	}
	if len(p.Value) == 0 {
		return nil, apperr.Validation("secret value cannot be empty", nil)
	}
	if len(p.Value) > MaxValueBytes {
		return nil, apperr.Validation("secret value too large", nil)
	}
	ct, nonce, err := s.box.Seal([]byte(p.Value))
	if err != nil {
		return nil, apperr.Internal(err)
	}

	sec := &model.Secret{
		ID:        uuid.New(),
		OrgID:     p.OrgID,
		ProjectID: p.ProjectID,
		Name:      name,
	}
	// Two partial unique indexes back the two scopes, so the UPSERT target
	// differs. On conflict we replace the ciphertext and bump updated_at,
	// keeping the original id/created_at.
	var createdByArg any
	if p.CreatedBy != uuid.Nil {
		createdByArg = p.CreatedBy
	}
	if p.ProjectID == nil {
		sec.Scope = "org"
		err = s.pool.QueryRow(ctx, `
			INSERT INTO secrets (id, org_id, project_id, scope, name, ciphertext, nonce, created_by)
			VALUES ($1, $2, NULL, 'org', $3, $4, $5, $6)
			ON CONFLICT (org_id, name) WHERE project_id IS NULL
			DO UPDATE SET ciphertext = EXCLUDED.ciphertext,
			              nonce      = EXCLUDED.nonce,
			              updated_at = now()
			RETURNING id, scope, created_at, updated_at
		`, sec.ID, p.OrgID, name, ct, nonce, createdByArg).
			Scan(&sec.ID, &sec.Scope, &sec.CreatedAt, &sec.UpdatedAt)
	} else {
		sec.Scope = "project"
		err = s.pool.QueryRow(ctx, `
			INSERT INTO secrets (id, org_id, project_id, scope, name, ciphertext, nonce, created_by)
			VALUES ($1, $2, $3, 'project', $4, $5, $6, $7)
			ON CONFLICT (project_id, name) WHERE project_id IS NOT NULL
			DO UPDATE SET ciphertext = EXCLUDED.ciphertext,
			              nonce      = EXCLUDED.nonce,
			              updated_at = now()
			RETURNING id, scope, created_at, updated_at
		`, sec.ID, p.OrgID, *p.ProjectID, name, ct, nonce, createdByArg).
			Scan(&sec.ID, &sec.Scope, &sec.CreatedAt, &sec.UpdatedAt)
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return sec, nil
}

// ListOrg returns metadata for an org's org-scoped secrets (no project ones).
func (s *Store) ListOrg(ctx context.Context, orgID uuid.UUID) ([]model.Secret, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, scope, name, created_at, updated_at
		FROM secrets WHERE org_id = $1 AND project_id IS NULL
		ORDER BY name ASC
	`, orgID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return scanSecrets(rows)
}

// ListProject returns metadata for a project's project-scoped secrets.
func (s *Store) ListProject(ctx context.Context, projectID uuid.UUID) ([]model.Secret, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, scope, name, created_at, updated_at
		FROM secrets WHERE project_id = $1
		ORDER BY name ASC
	`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return scanSecrets(rows)
}

// DeleteOrg removes an org-scoped secret by name.
func (s *Store) DeleteOrg(ctx context.Context, orgID uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM secrets WHERE org_id = $1 AND project_id IS NULL AND name = $2`,
		orgID, name)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("secret")
	}
	return nil
}

// DeleteProject removes a project-scoped secret by name.
func (s *Store) DeleteProject(ctx context.Context, projectID uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM secrets WHERE project_id = $1 AND name = $2`, projectID, name)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("secret")
	}
	return nil
}

// ResolveForProject returns the decrypted effective secret set for a project:
// the org's secrets overlaid by the project's (project wins on name clash).
// This is the map injected into a job's environment at acquire time.
func (s *Store) ResolveForProject(ctx context.Context, orgID, projectID uuid.UUID) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, ciphertext, nonce, (project_id IS NOT NULL) AS is_project
		FROM secrets
		WHERE (org_id = $1 AND project_id IS NULL) OR project_id = $2
	`, orgID, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()

	type entry struct {
		value     string
		isProject bool
	}
	merged := map[string]entry{}
	for rows.Next() {
		var name string
		var ct, nonce []byte
		var isProject bool
		if err := rows.Scan(&name, &ct, &nonce, &isProject); err != nil {
			return nil, apperr.Internal(err)
		}
		// Project scope always wins; never let an org secret overwrite a
		// project one regardless of row order.
		if cur, ok := merged[name]; ok && cur.isProject && !isProject {
			continue
		}
		pt, err := s.box.Open(ct, nonce)
		if err != nil {
			return nil, apperr.Internal(fmt.Errorf("decrypt secret %q: %w", name, err))
		}
		merged[name] = entry{value: string(pt), isProject: isProject}
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	out := make(map[string]string, len(merged))
	for name, e := range merged {
		out[name] = e.value
	}
	return out, nil
}

// GetOrgValue returns one decrypted org-scoped secret by name. Used by the
// autoscaler to fetch cloud credentials referenced by name in
// runner-config.yaml. Returns NotFound if absent.
func (s *Store) GetOrgValue(ctx context.Context, orgID uuid.UUID, name string) (string, error) {
	var ct, nonce []byte
	err := s.pool.QueryRow(ctx, `
		SELECT ciphertext, nonce FROM secrets
		WHERE org_id = $1 AND project_id IS NULL AND name = $2
	`, orgID, name).Scan(&ct, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.NotFound("secret " + name)
	}
	if err != nil {
		return "", apperr.Internal(err)
	}
	pt, err := s.box.Open(ct, nonce)
	if err != nil {
		return "", apperr.Internal(err)
	}
	return string(pt), nil
}

func scanSecrets(rows pgx.Rows) ([]model.Secret, error) {
	defer rows.Close()
	out := make([]model.Secret, 0)
	for rows.Next() {
		var sec model.Secret
		var projectID *uuid.UUID
		if err := rows.Scan(&sec.ID, &sec.OrgID, &projectID, &sec.Scope, &sec.Name, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		sec.ProjectID = projectID
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}
