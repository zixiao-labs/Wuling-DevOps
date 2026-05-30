package runnerstore

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// AutoscaleRunner is the autoscaler's view of an ephemeral runner: enough to
// decide idle scale-down and to terminate the backing cloud instance.
type AutoscaleRunner struct {
	ID         uuid.UUID
	Name       string
	PoolName   string
	Provider   string
	ExternalID string
	Status     string // offline|idle|busy
	LastJobAt  *time.Time
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

// CreateEphemeralRunner pre-provisions a runner row for an autoscaled instance
// and returns it with its raw wlrt_ token. The autoscaler injects the token
// via user-data so the VM authenticates directly (no register round-trip),
// which lets the autoscaler own the runner↔instance mapping (SetExternalID).
func (s *Store) CreateEphemeralRunner(ctx context.Context, orgID uuid.UUID, name string, labels []string, tier, provider, pool string) (*model.Runner, error) {
	if !validTier(tier) {
		tier = model.TierMedium
	}
	if name == "" {
		name = generatedRunnerName(provider, pool)
	}
	runnerID := uuid.New()
	rawTok, tokHash, err := newToken(RunnerTokenPrefix, runnerID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	r := &model.Runner{
		ID: runnerID, OrgID: orgID, Name: name, Labels: unionLabels(labels, nil),
		ResourceTier: tier, Provider: provider, PoolName: pool, Ephemeral: true,
		Status: "offline",
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO runners
		    (id, org_id, name, token_hash, labels, resource_tier, provider, pool_name, ephemeral, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,'offline')
		RETURNING created_at
	`, runnerID, orgID, name, tokHash, nonNilStrings(r.Labels), tier, provider, pool).Scan(&r.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "runner")
	}
	r.Token = rawTok
	return r, nil
}

// SetExternalID records the cloud instance id on a runner row so the
// autoscaler can terminate it later.
func (s *Store) SetExternalID(ctx context.Context, runnerID uuid.UUID, externalID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE runners SET external_id = $2 WHERE id = $1`, runnerID, externalID)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// ListForAutoscale returns the org's ephemeral runners for reconcile decisions.
func (s *Store) ListForAutoscale(ctx context.Context, orgID uuid.UUID) ([]AutoscaleRunner, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, pool_name, provider, external_id, status, last_job_at, last_seen_at, created_at
		FROM runners WHERE org_id = $1 AND ephemeral = TRUE
	`, orgID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]AutoscaleRunner, 0)
	for rows.Next() {
		var a AutoscaleRunner
		if err := rows.Scan(&a.ID, &a.Name, &a.PoolName, &a.Provider, &a.ExternalID,
			&a.Status, &a.LastJobAt, &a.LastSeenAt, &a.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OrgsWithEphemeralRunners lists orgs that currently have any ephemeral runner
// — the autoscaler must visit these even with an empty queue, to scale them
// down once idle.
func (s *Store) OrgsWithEphemeralRunners(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT org_id FROM runners WHERE ephemeral = TRUE`)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	return scanOrgIDs(rows)
}

func scanOrgIDs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
