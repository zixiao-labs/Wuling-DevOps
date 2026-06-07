// Package runnerstore is the persistence layer for runners and their
// registration tokens. Runners are ORG-SCOPED execution agents; there is no
// global pool. Tokens (wlrt_… for runners, wlreg_… for one-time registration)
// embed their row id so the resolver can load the row in O(1) and then
// argon2id-verify the secret half — the same hashing PATs use.
package runnerstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// Token prefixes. RunnerTokenPrefix is shared with the auth package so the git
// smart-HTTP handler can recognize a runner token; the reg prefix is local.
const (
	RunnerTokenPrefix = auth.RunnerTokenPrefix
	RegTokenPrefix    = "wlreg_"
)

// Store is the data-access object for runners.
type Store struct{ pool *db.Pool }

// New returns a Store backed by pool.
func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// RunnerIdentity is the resolved principal behind a runner token, attached to
// runner-API requests by the auth middleware.
type RunnerIdentity struct {
	RunnerID uuid.UUID
	OrgID    uuid.UUID
	Name     string
}

// RegistrationHints are copied from a registration token onto the runner row
// created when it is redeemed. The autoscaler sets these per launched VM.
type RegistrationHints struct {
	Labels       []string
	ResourceTier string
	Provider     string
	PoolName     string
	Ephemeral    bool
	ExternalID   string
}

// CreateRegistrationToken mints a single-use, short-lived registration token
// for an org. Returns the raw token (shown once). hints flow onto the runner.
func (s *Store) CreateRegistrationToken(ctx context.Context, orgID, createdBy uuid.UUID, h RegistrationHints, ttl time.Duration) (string, error) {
	tier := h.ResourceTier
	if !validTier(tier) {
		tier = model.TierMedium
	}
	provider := h.Provider
	if provider == "" {
		provider = "static"
	}
	id := uuid.New()
	raw, hash, err := newToken(RegTokenPrefix, id)
	if err != nil {
		return "", apperr.Internal(err)
	}
	var createdByArg any
	if createdBy != uuid.Nil {
		createdByArg = createdBy
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO runner_registration_tokens
		    (id, org_id, token_hash, labels, resource_tier, provider, pool_name, ephemeral, external_id, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, id, orgID, hash, nonNilStrings(h.Labels), tier, provider, h.PoolName, h.Ephemeral, h.ExternalID,
		createdByArg, time.Now().Add(ttl))
	if err != nil {
		return "", apperr.Internal(err)
	}
	return raw, nil
}

// RegisterParams are supplied by the runner client at registration time.
type RegisterParams struct {
	RawToken string   // the wlreg_… registration token
	Name     string   // runner-chosen name; generated if empty
	Labels   []string // extra labels unioned with the token's hint labels
}

// Register redeems a registration token and creates a runner, returning the
// runner plus its persistent token (raw, shown once). Single-use: the token is
// marked used in the same transaction.
func (s *Store) Register(ctx context.Context, p RegisterParams) (*model.Runner, error) {
	regID, ok := parseTokenID(RegTokenPrefix, p.RawToken)
	if !ok {
		return nil, apperr.Unauthorized("invalid registration token")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		orgID           uuid.UUID
		hash            string
		hintLabels      []string
		tier, provider  string
		poolName, extID string
		ephemeral       bool
		expiresAt       time.Time
		usedAt          *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT org_id, token_hash, labels, resource_tier, provider, pool_name, ephemeral, external_id, expires_at, used_at
		FROM runner_registration_tokens WHERE id = $1 FOR UPDATE
	`, regID).Scan(&orgID, &hash, &hintLabels, &tier, &provider, &poolName, &ephemeral, &extID, &expiresAt, &usedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Unauthorized("invalid registration token")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if usedAt != nil {
		return nil, apperr.Unauthorized("registration token already used")
	}
	if time.Now().After(expiresAt) {
		return nil, apperr.Unauthorized("registration token expired")
	}
	if ok, _ := auth.VerifyAccessToken(p.RawToken, hash); !ok {
		return nil, apperr.Unauthorized("invalid registration token")
	}

	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = generatedRunnerName(provider, poolName)
	}
	labels := unionLabels(hintLabels, p.Labels)

	runnerID := uuid.New()
	rawTok, tokHash, err := newToken(RunnerTokenPrefix, runnerID)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	r := &model.Runner{
		ID: runnerID, OrgID: orgID, Name: name, Labels: labels,
		ResourceTier: tier, Provider: provider, PoolName: poolName, Ephemeral: ephemeral,
		Status: "idle",
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO runners
		    (id, org_id, name, token_hash, labels, resource_tier, provider, pool_name, ephemeral, external_id, status, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'idle', now())
		RETURNING created_at, last_seen_at
	`, runnerID, orgID, name, tokHash, nonNilStrings(labels), tier, provider, poolName, ephemeral, extID).
		Scan(&r.CreatedAt, &r.LastSeenAt)
	if err != nil {
		return nil, mapInsertErr(err, "runner")
	}
	if _, err := tx.Exec(ctx,
		`UPDATE runner_registration_tokens SET used_at = now() WHERE id = $1`, regID); err != nil {
		return nil, apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	r.Token = rawTok
	return r, nil
}

// Resolve loads the identity behind a runner token. It is read-only; runner
// liveness is tracked by the explicit Heartbeat call, not as a side effect of
// every auth (this path also backs read-only git-over-HTTP for runners).
func (s *Store) Resolve(ctx context.Context, rawToken string) (*RunnerIdentity, error) {
	runnerID, ok := parseTokenID(RunnerTokenPrefix, rawToken)
	if !ok {
		return nil, apperr.Unauthorized("invalid runner token")
	}
	var (
		orgID uuid.UUID
		name  string
		hash  string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT org_id, name, token_hash FROM runners WHERE id = $1`, runnerID).
		Scan(&orgID, &name, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Unauthorized("invalid runner token")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if ok, _ := auth.VerifyAccessToken(rawToken, hash); !ok {
		return nil, apperr.Unauthorized("invalid runner token")
	}
	return &RunnerIdentity{RunnerID: runnerID, OrgID: orgID, Name: name}, nil
}

// ResolveRunnerToken adapts Resolve to the *auth.Identity shape the git
// smart-HTTP handler expects, so a runner can `git clone` over HTTP Basic with
// its token (read-only, scoped to its org — enforced in githttp). The identity
// carries no UserID; OrgID is the access principal.
func (s *Store) ResolveRunnerToken(ctx context.Context, rawToken string) (*auth.Identity, error) {
	ri, err := s.Resolve(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	return &auth.Identity{
		Source:   auth.IdentitySourceRunner,
		OrgID:    ri.OrgID,
		Username: ri.Name,
	}, nil
}

// Heartbeat updates last_seen_at and status. status must be idle|busy.
func (s *Store) Heartbeat(ctx context.Context, runnerID uuid.UUID, status string) error {
	if status != "idle" && status != "busy" {
		status = "idle"
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE runners SET last_seen_at = now(), status = $2 WHERE id = $1`, runnerID, status)
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// List returns an org's runners ordered by name.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]model.Runner, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, name, labels, resource_tier, provider, pool_name, ephemeral, status, last_seen_at, last_job_at, created_at
		FROM runners WHERE org_id = $1 ORDER BY LOWER(name) ASC
	`, orgID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.Runner, 0)
	for rows.Next() {
		var r model.Runner
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Name, &r.Labels, &r.ResourceTier, &r.Provider,
			&r.PoolName, &r.Ephemeral, &r.Status, &r.LastSeenAt, &r.LastJobAt, &r.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// Delete removes a runner (used by manual delete and ephemeral teardown).
func (s *Store) Delete(ctx context.Context, orgID, runnerID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM runners WHERE id = $1 AND org_id = $2`, runnerID, orgID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("runner")
	}
	return nil
}

// ----------------------------------------------------------------------------
// token helpers
// ----------------------------------------------------------------------------

func newToken(prefix string, id uuid.UUID) (raw, hash string, err error) {
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	raw = prefix + hex.EncodeToString(id[:]) + "_" + hex.EncodeToString(secret)
	hash, err = auth.HashPassword(raw)
	return raw, hash, err
}

// parseTokenID extracts the embedded uuid from prefix<hexid>_<secret>.
func parseTokenID(prefix, raw string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return uuid.Nil, false
	}
	idHex, _, ok := strings.Cut(rest, "_")
	if !ok || len(idHex) != 32 {
		return uuid.Nil, false
	}
	b, err := hex.DecodeString(idHex)
	if err != nil || len(b) != 16 {
		return uuid.Nil, false
	}
	var id uuid.UUID
	copy(id[:], b)
	return id, true
}

func generatedRunnerName(provider, pool string) string {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	base := provider
	if pool != "" {
		base = pool
	}
	if base == "" {
		base = "runner"
	}
	return fmt.Sprintf("%s-%s", base, hex.EncodeToString(suffix))
}

func unionLabels(a, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, l := range append(append([]string{}, a...), b...) {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	return out
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func validTier(t string) bool {
	return t == model.TierLow || t == model.TierMedium || t == model.TierHigh
}

func mapInsertErr(err error, kind string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return apperr.Conflict(fmt.Sprintf("%s already exists", kind))
		case "23503":
			return apperr.New(apperr.CodeBadRequest, fmt.Sprintf("invalid reference creating %s", kind))
		}
	}
	return apperr.Internal(err)
}
