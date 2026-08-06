package runnercheck

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/secretbox"
)

// AuditStore persists the non-secret lifecycle and the encrypted one-time
// sentinel used by a real Runner self-check. It is deliberately separate from
// normal organization secrets: a diagnostic must never resolve or inject the
// user's existing Secret set.
type AuditStore struct {
	pool *db.Pool
	box  *secretbox.Box
	now  func() time.Time
}

// NewAuditStore creates a durable self-check store. A nil box is invalid for
// writes; callers should use the same AES-GCM box as secretstore.
func NewAuditStore(pool *db.Pool, box *secretbox.Box) *AuditStore {
	return &AuditStore{pool: pool, box: box, now: time.Now}
}

// AuditRecord is safe to return to a global administrator. It intentionally
// excludes the encrypted sentinel and its hash.
type AuditRecord struct {
	ID           uuid.UUID       `json:"id"`
	OrgID        uuid.UUID       `json:"org_id"`
	RequestedBy  uuid.UUID       `json:"requested_by"`
	PoolName     string          `json:"pool_name"`
	Provider     string          `json:"provider"`
	OS           string          `json:"os"`
	Phase        LifecyclePhase  `json:"phase"`
	State        ExecutionState  `json:"state"`
	Checks       json.RawMessage `json:"checks"`
	Summary      string          `json:"summary,omitempty"`
	RunID        *uuid.UUID      `json:"run_id,omitempty"`
	JobID        *uuid.UUID      `json:"job_id,omitempty"`
	RunnerID     *uuid.UUID      `json:"runner_id,omitempty"`
	ExternalID   string          `json:"external_id,omitempty"`
	CleanupTries int             `json:"cleanup_attempts"`
	CleanupError string          `json:"cleanup_last_error,omitempty"`
	NextCleanup  *time.Time      `json:"next_cleanup_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
	CleanedAt    *time.Time      `json:"cleaned_at,omitempty"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// CreateAuditParams is the per-pool durable input. ProbeSecret is encrypted
// before it reaches PostgreSQL and is never copied into AuditRecord.
type CreateAuditParams struct {
	OrgID       uuid.UUID
	RequestedBy uuid.UUID
	PoolName    string
	Provider    string
	OS          string
	Checks      []Check
	ProbeSecret string
}

// Create creates an initial, preflight-complete audit record.
func (s *AuditStore) Create(ctx context.Context, p CreateAuditParams) (*AuditRecord, error) {
	if s == nil || s.pool == nil || s.box == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "runner self-check durable storage is unavailable")
	}
	if p.OrgID == uuid.Nil || p.RequestedBy == uuid.Nil || p.PoolName == "" || p.Provider == "" || p.OS == "" {
		return nil, apperr.Validation("runner self-check requires organization, requester, pool, provider, and OS", nil)
	}
	if p.ProbeSecret == "" {
		return nil, apperr.Validation("runner self-check probe secret cannot be empty", nil)
	}
	checks, err := json.Marshal(p.Checks)
	if err != nil {
		return nil, apperr.Internal(fmt.Errorf("encode self-check result: %w", err))
	}
	ciphertext, nonce, err := s.box.Seal([]byte(p.ProbeSecret))
	if err != nil {
		return nil, apperr.Internal(err)
	}
	hash := sha256.Sum256([]byte(p.ProbeSecret))
	now := s.clock().UTC()
	record := &AuditRecord{
		ID:          uuid.New(),
		OrgID:       p.OrgID,
		RequestedBy: p.RequestedBy,
		PoolName:    p.PoolName,
		Provider:    p.Provider,
		OS:          p.OS,
		Phase:       PhasePreflight,
		State:       StatePreflight,
		Checks:      checks,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO runner_self_checks (
			id, org_id, requested_by, pool_name, provider, os, phase, state,
			checks, secret_ciphertext, secret_nonce, secret_hash, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13,$13)
		RETURNING created_at, updated_at
	`, record.ID, record.OrgID, record.RequestedBy, record.PoolName, record.Provider, record.OS,
		record.Phase, record.State, string(record.Checks), ciphertext, nonce, hash[:], now).
		Scan(&record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, apperr.Conflict("an active runner self-check already exists for this pool")
		}
		return nil, apperr.Internal(err)
	}
	return record, nil
}

// List returns newest-first records for an organization.
func (s *AuditStore) List(ctx context.Context, orgID uuid.UUID, limit int) ([]AuditRecord, error) {
	if s == nil || s.pool == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "runner self-check durable storage is unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, requested_by, pool_name, provider, os, phase, state, checks,
		       summary, run_id, job_id, runner_id, external_id, cleanup_attempts,
		       cleanup_last_error, next_cleanup_at, created_at, started_at, finished_at,
		       cleaned_at, updated_at
		FROM runner_self_checks
		WHERE org_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, orgID, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]AuditRecord, 0)
	for rows.Next() {
		record, err := scanAuditRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// HasActive reports whether a pool still owns a self-check lifecycle that has
// not reached resource cleanup. It is used before queueing a multi-pool request
// so a rejected duplicate cannot partially create other billable probes.
func (s *AuditStore) HasActive(ctx context.Context, orgID uuid.UUID, poolName string) (bool, error) {
	if s == nil || s.pool == nil {
		return false, apperr.New(apperr.CodeUnavailable, "runner self-check durable storage is unavailable")
	}
	var active bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM runner_self_checks
			WHERE org_id = $1 AND pool_name = $2 AND cleaned_at IS NULL
		)
	`, orgID, poolName).Scan(&active); err != nil {
		return false, apperr.Internal(err)
	}
	return active, nil
}

// GetByJob returns a record assigned to an internal probe job.
func (s *AuditStore) GetByJob(ctx context.Context, jobID uuid.UUID) (*AuditRecord, error) {
	if s == nil || s.pool == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "runner self-check durable storage is unavailable")
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, org_id, requested_by, pool_name, provider, os, phase, state, checks,
		       summary, run_id, job_id, runner_id, external_id, cleanup_attempts,
		       cleanup_last_error, next_cleanup_at, created_at, started_at, finished_at,
		       cleaned_at, updated_at
		FROM runner_self_checks WHERE job_id = $1
	`, jobID)
	record, err := scanAuditRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("runner self-check")
	}
	return record, err
}

// ProbeSecret decrypts only the sentinel for a known audit ID. Its caller is
// the runner acquire path after it has claimed the internal probe job.
func (s *AuditStore) ProbeSecret(ctx context.Context, id uuid.UUID) (string, error) {
	if s == nil || s.pool == nil || s.box == nil {
		return "", apperr.New(apperr.CodeUnavailable, "runner self-check durable storage is unavailable")
	}
	var ciphertext, nonce []byte
	err := s.pool.QueryRow(ctx, `
		SELECT secret_ciphertext, secret_nonce
		FROM runner_self_checks
		WHERE id = $1
		  AND state IN ('queued','provisioning','waiting_for_runner','executing')
	`, id).Scan(&ciphertext, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.NotFound("runner self-check probe")
	}
	if err != nil {
		return "", apperr.Internal(err)
	}
	plain, err := s.box.Open(ciphertext, nonce)
	if err != nil {
		return "", apperr.Internal(fmt.Errorf("decrypt runner self-check probe: %w", err))
	}
	return string(plain), nil
}

// LinkPipeline records the synthetic internal run/job after durable preflight
// succeeds. It also moves the check into the queue visible to the autoscaler.
func (s *AuditStore) LinkPipeline(ctx context.Context, id, runID, jobID uuid.UUID) error {
	return s.update(ctx, id, `
		run_id = $2, job_id = $3, phase = 'provision', state = 'queued',
		started_at = COALESCE(started_at, now()), updated_at = now()
	`, runID, jobID)
}

// MarkStartFailed closes an audit before an internal pipeline job exists. No
// provider call was made in this path, so shredding the one-time value is safe.
func (s *AuditStore) MarkStartFailed(ctx context.Context, id uuid.UUID, summary string) error {
	return s.update(ctx, id, `
		phase = 'cleanup', state = 'failed', summary = $2,
		secret_ciphertext = NULL, secret_nonce = NULL,
		finished_at = now(), cleaned_at = now(), updated_at = now()
	`, truncateSummary(summary))
}

// MarkProvisioned stores the provider mapping before waiting for registration.
func (s *AuditStore) MarkProvisioned(ctx context.Context, jobID, runnerID uuid.UUID, externalID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE runner_self_checks
		SET runner_id = $2, external_id = $3, phase = 'wait_runner',
		    state = 'waiting_for_runner', updated_at = now()
		WHERE job_id = $1
	`, jobID, runnerID, externalID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("runner self-check")
	}
	return nil
}

// MarkExecuting records that the reserved Runner acquired the probe job.
func (s *AuditStore) MarkExecuting(ctx context.Context, jobID uuid.UUID) error {
	return s.updateByJob(ctx, jobID, `
		phase = 'execute', state = 'executing', updated_at = now()
	`)
}

// MarkExecutingForRunner also captures the selected runner identity. It is
// called from the authenticated acquire path, after the dispatcher has claimed
// the one isolated job for that runner.
func (s *AuditStore) MarkExecutingForRunner(ctx context.Context, jobID, runnerID uuid.UUID) error {
	return s.updateByJob(ctx, jobID, `
		runner_id = $2,
		external_id = COALESCE(
			(SELECT NULLIF(external_id, '') FROM runners WHERE id = $2),
			runner_self_checks.external_id
		),
		phase = 'execute', state = 'executing', updated_at = now()
	`, runnerID)
}

// MarkProbeFinished preserves the command result while intentionally retaining
// the encrypted sentinel until cleanup completes; retries can then use the
// same job safely without generating or exposing a user secret.
func (s *AuditStore) MarkProbeFinished(ctx context.Context, jobID uuid.UUID, succeeded bool, summary string) error {
	state := StateFailed
	if succeeded {
		state = StateSucceeded
	}
	return s.updateByJob(ctx, jobID, `
		phase = 'cleanup', state = $2, summary = $3, finished_at = now(), updated_at = now()
	`, state, truncateSummary(summary))
}

// MarkCleanupPending retains the external id and queues another cleanup
// attempt. It is safe to call repeatedly after crashes or provider errors.
func (s *AuditStore) MarkCleanupPending(ctx context.Context, jobID uuid.UUID, reason string, next time.Time) error {
	return s.updateByJob(ctx, jobID, `
		phase = 'cleanup', state = 'cleanup_pending', cleanup_attempts = cleanup_attempts + 1,
		cleanup_last_error = $2, next_cleanup_at = $3, updated_at = now()
	`, truncateSummary(reason), next.UTC())
}

// MarkCleaned finalizes the audit after its VM/runner mapping is gone. The
// encrypted sentinel is shredded from the row at this point.
func (s *AuditStore) MarkCleaned(ctx context.Context, jobID uuid.UUID) error {
	return s.updateByJob(ctx, jobID, `
		phase = 'cleanup', state = 'cleaned', next_cleanup_at = NULL,
		cleanup_last_error = '', secret_ciphertext = NULL, secret_nonce = NULL,
		cleaned_at = now(), updated_at = now()
	`)
}

// AppendChecks atomically extends the safe, administrator-visible check list.
// It never accepts user-provided log text; callers pass fixed status messages.
func (s *AuditStore) AppendChecks(ctx context.Context, jobID uuid.UUID, additional []Check) error {
	if len(additional) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var raw []byte
	if err := tx.QueryRow(ctx, `
		SELECT checks FROM runner_self_checks WHERE job_id = $1 FOR UPDATE
	`, jobID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound("runner self-check")
		}
		return apperr.Internal(err)
	}
	var current []Check
	if err := json.Unmarshal(raw, &current); err != nil {
		return apperr.Internal(fmt.Errorf("decode runner self-check checks: %w", err))
	}
	current = append(current, additional...)
	encoded, err := json.Marshal(current)
	if err != nil {
		return apperr.Internal(fmt.Errorf("encode runner self-check checks: %w", err))
	}
	if _, err := tx.Exec(ctx, `
		UPDATE runner_self_checks SET checks = $2::jsonb, updated_at = now()
		WHERE job_id = $1
	`, jobID, string(encoded)); err != nil {
		return apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// The following methods implement autoscale.IsolatedLifecycle. An arbitrary
// isolated workflow job is not necessarily a self-check, so a missing audit
// record is deliberately a no-op rather than an error.

func (s *AuditStore) MarkIsolatedProvisioned(ctx context.Context, jobID, runnerID uuid.UUID, externalID string) error {
	return s.updateByJobIfPresent(ctx, jobID, `
		runner_id = $2, external_id = $3, phase = 'wait_runner',
		state = 'waiting_for_runner', started_at = COALESCE(started_at, now()),
		updated_at = now()
	`, runnerID, externalID)
}

func (s *AuditStore) MarkIsolatedProvisioningFailure(ctx context.Context, jobID uuid.UUID, summary string) error {
	return s.updateByJobIfPresent(ctx, jobID, `
		phase = 'provision', state = 'queued', summary = $2, updated_at = now()
	`, truncateSummary(summary))
}

func (s *AuditStore) MarkIsolatedCleanupPending(ctx context.Context, jobID uuid.UUID, summary string, next time.Time) error {
	return s.updateByJobIfPresent(ctx, jobID, `
		phase = 'cleanup', state = 'cleanup_pending', cleanup_attempts = cleanup_attempts + 1,
		cleanup_last_error = $2, next_cleanup_at = $3, updated_at = now()
	`, truncateSummary(summary), next.UTC())
}

func (s *AuditStore) MarkIsolatedCleaned(ctx context.Context, jobID uuid.UUID) error {
	return s.updateByJobIfPresent(ctx, jobID, `
		phase = 'cleanup', state = 'cleaned', next_cleanup_at = NULL,
		cleanup_last_error = '', secret_ciphertext = NULL, secret_nonce = NULL,
		cleaned_at = now(), updated_at = now()
	`)
}

func (s *AuditStore) update(ctx context.Context, id uuid.UUID, set string, args ...any) error {
	query := `UPDATE runner_self_checks SET ` + set + ` WHERE id = $1`
	params := append([]any{id}, args...)
	tag, err := s.pool.Exec(ctx, query, params...)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("runner self-check")
	}
	return nil
}

func (s *AuditStore) updateByJob(ctx context.Context, jobID uuid.UUID, set string, args ...any) error {
	query := `UPDATE runner_self_checks SET ` + set + ` WHERE job_id = $1`
	params := append([]any{jobID}, args...)
	tag, err := s.pool.Exec(ctx, query, params...)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("runner self-check")
	}
	return nil
}

func (s *AuditStore) updateByJobIfPresent(ctx context.Context, jobID uuid.UUID, set string, args ...any) error {
	query := `UPDATE runner_self_checks SET ` + set + ` WHERE job_id = $1`
	params := append([]any{jobID}, args...)
	if _, err := s.pool.Exec(ctx, query, params...); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (s *AuditStore) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func truncateSummary(s string) string {
	const max = 1024
	if len(s) <= max {
		return s
	}
	if utf8.ValidString(s[:max]) {
		return s[:max] + "…"
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	if i == 0 {
		return "…"
	}
	return s[:i] + "…"
}

type auditScanner interface {
	Scan(...any) error
}

func scanAuditRecord(scanner auditScanner) (*AuditRecord, error) {
	var record AuditRecord
	var raw []byte
	if err := scanner.Scan(
		&record.ID, &record.OrgID, &record.RequestedBy, &record.PoolName, &record.Provider, &record.OS,
		&record.Phase, &record.State, &raw, &record.Summary, &record.RunID, &record.JobID,
		&record.RunnerID, &record.ExternalID, &record.CleanupTries, &record.CleanupError,
		&record.NextCleanup, &record.CreatedAt, &record.StartedAt, &record.FinishedAt,
		&record.CleanedAt, &record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	record.Checks = append(record.Checks[:0], raw...)
	return &record, nil
}
