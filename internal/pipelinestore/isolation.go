package pipelinestore

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// IsolatedJobStatus is the reconciliation view of a strong-isolation job.
// It deliberately contains only lifecycle metadata, never its job definition
// or secrets.
type IsolatedJobStatus struct {
	JobID            uuid.UUID
	OrgID            uuid.UUID
	Pool             string
	Status           string
	RunnerID         *uuid.UUID
	ReservedRunnerID *uuid.UUID
}

// Terminal reports whether no Runner may execute this job again.
func (s IsolatedJobStatus) Terminal() bool {
	switch s.Status {
	case "success", "failed", "canceled":
		return true
	default:
		return false
	}
}

// GetIsolatedJobStatus returns the durable reservation state for one
// autoscaled strong-isolation VM.
func (s *Store) GetIsolatedJobStatus(ctx context.Context, jobID uuid.UUID) (*IsolatedJobStatus, error) {
	var status IsolatedJobStatus
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, execution_pool, status, runner_id, reserved_runner_id
		FROM pipeline_jobs
		WHERE id = $1 AND execution_mode = 'isolated'
	`, jobID).Scan(
		&status.JobID, &status.OrgID, &status.Pool, &status.Status,
		&status.RunnerID, &status.ReservedRunnerID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("isolated pipeline job")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &status, nil
}
