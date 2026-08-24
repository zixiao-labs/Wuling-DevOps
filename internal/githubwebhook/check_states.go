package githubwebhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/db"
)

const (
	CheckColorGreen = "green"
	CheckColorRed   = "red"
)

// CheckCompletion is the normalized terminal state of a GitHub Actions
// workflow or Checks check run.
type CheckCompletion struct {
	ID          uuid.UUID
	RepoID      uuid.UUID
	Source      string
	ExternalID  string
	Name        string
	Provider    string
	HeadSHA     string
	Conclusion  string
	Color       string
	Attempt     int
	DetailsURL  string
	CompletedAt time.Time
}

// CheckStore keeps the latest terminal state for each GitHub check resource.
type CheckStore struct {
	Pool *db.Pool
}

// Upsert records a completed check. Re-runs update the same logical resource
// while the notification dedupe key preserves distinct attempts/completions.
func (s *CheckStore) Upsert(ctx context.Context, check CheckCompletion) (*CheckCompletion, error) {
	if s == nil || s.Pool == nil {
		return nil, fmt.Errorf("github check store is not configured")
	}
	if check.ID == uuid.Nil {
		check.ID = uuid.New()
	}
	if check.Attempt < 1 {
		check.Attempt = 1
	}
	if check.CompletedAt.IsZero() {
		check.CompletedAt = time.Now().UTC()
	}
	if check.Color == "" {
		check.Color = NormalizeCheckColor(check.Conclusion)
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO github_check_states
			(id, repo_id, source, external_id, name, provider, head_sha,
			 status, conclusion, color, attempt, details_url, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7,
		        'completed', $8, $9, $10, $11, $12)
		ON CONFLICT (repo_id, source, external_id) DO UPDATE SET
			name = EXCLUDED.name,
			provider = EXCLUDED.provider,
			head_sha = EXCLUDED.head_sha,
			status = 'completed',
			conclusion = EXCLUDED.conclusion,
			color = EXCLUDED.color,
			attempt = EXCLUDED.attempt,
			details_url = EXCLUDED.details_url,
			completed_at = EXCLUDED.completed_at,
			updated_at = now()
		RETURNING id, repo_id, source, external_id, name, provider, head_sha,
		          conclusion, color, attempt, details_url, completed_at
	`, check.ID, check.RepoID, check.Source, check.ExternalID, check.Name,
		check.Provider, check.HeadSHA, check.Conclusion, check.Color, check.Attempt,
		check.DetailsURL, check.CompletedAt)
	var saved CheckCompletion
	if err := row.Scan(&saved.ID, &saved.RepoID, &saved.Source, &saved.ExternalID,
		&saved.Name, &saved.Provider, &saved.HeadSHA, &saved.Conclusion,
		&saved.Color, &saved.Attempt, &saved.DetailsURL, &saved.CompletedAt); err != nil {
		return nil, fmt.Errorf("upsert github check state: %w", err)
	}
	return &saved, nil
}

// Get returns the latest terminal state for a GitHub resource.
func (s *CheckStore) Get(ctx context.Context, repoID uuid.UUID, source, externalID string) (*CheckCompletion, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, repo_id, source, external_id, name, provider, head_sha,
		       conclusion, color, attempt, details_url, completed_at
		FROM github_check_states
		WHERE repo_id = $1 AND source = $2 AND external_id = $3
	`, repoID, source, externalID)
	var check CheckCompletion
	if err := row.Scan(&check.ID, &check.RepoID, &check.Source, &check.ExternalID,
		&check.Name, &check.Provider, &check.HeadSHA, &check.Conclusion,
		&check.Color, &check.Attempt, &check.DetailsURL, &check.CompletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get github check state: %w", err)
	}
	return &check, nil
}

// NormalizeCheckColor maps GitHub terminal conclusions to Wuling's compact
// red/green rollup. Only an explicit success is green; failures, cancellation,
// neutral/skipped, and unknown future values stay non-green.
func NormalizeCheckColor(conclusion string) string {
	if conclusion == "success" {
		return CheckColorGreen
	}
	return CheckColorRed
}
