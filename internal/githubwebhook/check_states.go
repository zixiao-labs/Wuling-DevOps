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
	// FeedbackCheckRunID is the Wuling-owned GitHub Check Run that mirrors this
	// observed external result. Zero means no feedback has been created yet.
	FeedbackCheckRunID int64
}

// CheckStore keeps the latest terminal state for each GitHub check resource.
type CheckStore struct {
	Pool *db.Pool
}

type checkQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Upsert records a completed check. Only a higher attempt, or a later
// completion within the same attempt, replaces the current state. Older
// out-of-order deliveries return the existing row unchanged.
func (s *CheckStore) Upsert(ctx context.Context, check CheckCompletion) (*CheckCompletion, error) {
	if s == nil || s.Pool == nil {
		return nil, fmt.Errorf("github check store is not configured")
	}
	return upsertCheck(ctx, s.Pool, check)
}

// UpsertTx is Upsert using the caller's transaction. It lets check state and
// its notification outbox event commit atomically.
func (s *CheckStore) UpsertTx(ctx context.Context, tx pgx.Tx, check CheckCompletion) (*CheckCompletion, error) {
	if s == nil || tx == nil {
		return nil, fmt.Errorf("github check transaction is not configured")
	}
	return upsertCheck(ctx, tx, check)
}

func upsertCheck(ctx context.Context, q checkQueryRower, check CheckCompletion) (*CheckCompletion, error) {
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
	row := q.QueryRow(ctx, `
		INSERT INTO github_check_states
			(id, repo_id, source, external_id, name, provider, head_sha,
			 status, conclusion, color, attempt, details_url, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7,
		        'completed', $8, $9, $10, $11, $12)
		ON CONFLICT (repo_id, source, external_id) DO UPDATE SET
			name = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                      (github_check_states.attempt, github_check_states.completed_at)
			            THEN EXCLUDED.name ELSE github_check_states.name END,
			provider = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                          (github_check_states.attempt, github_check_states.completed_at)
			                THEN EXCLUDED.provider ELSE github_check_states.provider END,
			head_sha = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                          (github_check_states.attempt, github_check_states.completed_at)
			                THEN EXCLUDED.head_sha ELSE github_check_states.head_sha END,
			status = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                        (github_check_states.attempt, github_check_states.completed_at)
			              THEN EXCLUDED.status ELSE github_check_states.status END,
			conclusion = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                            (github_check_states.attempt, github_check_states.completed_at)
			                  THEN EXCLUDED.conclusion ELSE github_check_states.conclusion END,
			color = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                       (github_check_states.attempt, github_check_states.completed_at)
			             THEN EXCLUDED.color ELSE github_check_states.color END,
			attempt = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                         (github_check_states.attempt, github_check_states.completed_at)
			               THEN EXCLUDED.attempt ELSE github_check_states.attempt END,
			details_url = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                             (github_check_states.attempt, github_check_states.completed_at)
			                   THEN EXCLUDED.details_url ELSE github_check_states.details_url END,
			completed_at = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                              (github_check_states.attempt, github_check_states.completed_at)
			                    THEN EXCLUDED.completed_at ELSE github_check_states.completed_at END,
			updated_at = CASE WHEN (EXCLUDED.attempt, EXCLUDED.completed_at) >
			                            (github_check_states.attempt, github_check_states.completed_at)
			                  THEN now() ELSE github_check_states.updated_at END
		RETURNING id, repo_id, source, external_id, name, provider, head_sha,
		          conclusion, color, attempt, details_url, completed_at,
		          COALESCE(feedback_check_run_id, 0)
	`, check.ID, check.RepoID, check.Source, check.ExternalID, check.Name,
		check.Provider, check.HeadSHA, check.Conclusion, check.Color, check.Attempt,
		check.DetailsURL, check.CompletedAt)
	var saved CheckCompletion
	if err := row.Scan(&saved.ID, &saved.RepoID, &saved.Source, &saved.ExternalID,
		&saved.Name, &saved.Provider, &saved.HeadSHA, &saved.Conclusion,
		&saved.Color, &saved.Attempt, &saved.DetailsURL, &saved.CompletedAt,
		&saved.FeedbackCheckRunID); err != nil {
		return nil, fmt.Errorf("upsert github check state: %w", err)
	}
	return &saved, nil
}

// Get returns the latest terminal state for a GitHub resource.
func (s *CheckStore) Get(ctx context.Context, repoID uuid.UUID, source, externalID string) (*CheckCompletion, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, repo_id, source, external_id, name, provider, head_sha,
		       conclusion, color, attempt, details_url, completed_at,
		       COALESCE(feedback_check_run_id, 0)
		FROM github_check_states
		WHERE repo_id = $1 AND source = $2 AND external_id = $3
	`, repoID, source, externalID)
	var check CheckCompletion
	if err := row.Scan(&check.ID, &check.RepoID, &check.Source, &check.ExternalID,
		&check.Name, &check.Provider, &check.HeadSHA, &check.Conclusion,
		&check.Color, &check.Attempt, &check.DetailsURL, &check.CompletedAt,
		&check.FeedbackCheckRunID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get github check state: %w", err)
	}
	return &check, nil
}

// SetFeedbackCheckRunID links an observed external result to the Wuling-owned
// Check Run created for it, making webhook retries update rather than duplicate
// the branch-protection context.
func (s *CheckStore) SetFeedbackCheckRunID(ctx context.Context, id uuid.UUID, checkRunID int64) error {
	if s == nil || s.Pool == nil {
		return fmt.Errorf("github check store is not configured")
	}
	if id == uuid.Nil || checkRunID <= 0 {
		return fmt.Errorf("invalid github feedback check run link")
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE github_check_states
		SET feedback_check_run_id = $1, updated_at = now()
		WHERE id = $2
	`, checkRunID, id)
	if err != nil {
		return fmt.Errorf("link github feedback check run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("link github feedback check run: check state not found")
	}
	return nil
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
