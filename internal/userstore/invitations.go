// Invitations store helpers.
//
// Invitations are write-once-read-many: created by an admin (token returned
// only at creation time), accepted by the recipient (a single transaction
// inserts org_members and flips invitation.status to 'accepted'), or
// revoked / expired through ordinary lifecycle queries.

package userstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// CreateInvitationParams holds inputs to CreateInvitation. Exactly one of
// InviteeUserID and InviteeEmail must be non-zero; the table CHECK enforces
// it but we validate at the Go layer to surface a clean message.
type CreateInvitationParams struct {
	OrgID          uuid.UUID
	InviterUserID  uuid.UUID
	InviteeUserID  *uuid.UUID
	InviteeEmail   string
	Role           string
	TokenHash      string
	TTL            time.Duration
}

// CreateInvitation inserts an invitation row. Returns Conflict if a pending
// invitation already exists for the same (org, invitee) pair.
func (s *Store) CreateInvitation(ctx context.Context, p CreateInvitationParams) (*model.OrgInvitation, error) {
	if p.InviteeUserID == nil && strings.TrimSpace(p.InviteeEmail) == "" {
		return nil, apperr.Validation("invitee_user_id or invitee_email is required", nil)
	}
	if !isInvitableStoredRole(p.Role) {
		return nil, apperr.Validation("invalid role for invitation", map[string]any{
			"role": "must be one of maintainer/developer/reporter/guest",
		})
	}
	if p.TTL <= 0 {
		p.TTL = 7 * 24 * time.Hour
	}

	id := uuid.New()
	expiresAt := time.Now().UTC().Add(p.TTL)
	var inviteeUserID any
	if p.InviteeUserID != nil {
		inviteeUserID = *p.InviteeUserID
	}
	var inviteeEmail any
	if e := strings.ToLower(strings.TrimSpace(p.InviteeEmail)); e != "" {
		inviteeEmail = e
	}

	var inv model.OrgInvitation
	err := s.pool.QueryRow(ctx, `
		INSERT INTO org_invitations
		    (id, org_id, inviter_user_id, invitee_user_id, invitee_email,
		     role, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, org_id, role, status, expires_at, created_at
	`, id, p.OrgID, p.InviterUserID, inviteeUserID, inviteeEmail,
		p.Role, p.TokenHash, expiresAt).Scan(
		&inv.ID, &inv.OrgID, &inv.Role, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "invitation")
	}
	inv.InviteeUserID = p.InviteeUserID
	if e, ok := inviteeEmail.(string); ok {
		inv.InviteeEmail = e
	}
	return &inv, nil
}

// ListInvitations returns all invitations for an org, ordered newest-first.
// status filters when non-empty (e.g. "pending").
func (s *Store) ListInvitations(ctx context.Context, orgID uuid.UUID, status string) ([]model.OrgInvitation, error) {
	var rows pgx.Rows
	var err error
	q := `
		SELECT i.id, i.org_id, i.invitee_user_id, i.invitee_email, i.role, i.status,
		       i.expires_at, i.created_at, i.accepted_at,
		       inviter.id, inviter.username, inviter.display_name
		  FROM org_invitations i
		  JOIN users inviter ON inviter.id = i.inviter_user_id
		 WHERE i.org_id = $1
	`
	if status != "" {
		rows, err = s.pool.Query(ctx, q+` AND i.status = $2 ORDER BY i.created_at DESC`, orgID, status)
	} else {
		rows, err = s.pool.Query(ctx, q+` ORDER BY i.created_at DESC`, orgID)
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := []model.OrgInvitation{}
	for rows.Next() {
		var inv model.OrgInvitation
		var invitee *uuid.UUID
		var inviteeEmail *string
		var acceptedAt *time.Time
		var inviterID uuid.UUID
		var inviterUsername, inviterDisplay string
		if err := rows.Scan(&inv.ID, &inv.OrgID, &invitee, &inviteeEmail, &inv.Role, &inv.Status,
			&inv.ExpiresAt, &inv.CreatedAt, &acceptedAt,
			&inviterID, &inviterUsername, &inviterDisplay); err != nil {
			return nil, apperr.Internal(err)
		}
		inv.InviteeUserID = invitee
		if inviteeEmail != nil {
			inv.InviteeEmail = *inviteeEmail
		}
		inv.AcceptedAt = acceptedAt
		inv.Inviter = &model.UserRef{ID: inviterID, Username: inviterUsername, DisplayName: inviterDisplay}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// GetInvitationByTokenHash resolves a (constant-time-hashed) token to the
// invitation row plus the embedded org slug. Returns NotFound for unknown
// tokens to avoid leaking which tokens previously existed. Expired/revoked/
// accepted invitations are returned with their current status — the caller
// is responsible for refusing to accept them.
func (s *Store) GetInvitationByTokenHash(ctx context.Context, tokenHash string) (*model.OrgInvitation, error) {
	var inv model.OrgInvitation
	var invitee *uuid.UUID
	var inviteeEmail *string
	var acceptedAt *time.Time
	var inviterID uuid.UUID
	var inviterUsername, inviterDisplay string
	err := s.pool.QueryRow(ctx, `
		SELECT i.id, i.org_id, i.invitee_user_id, i.invitee_email, i.role, i.status,
		       i.expires_at, i.created_at, i.accepted_at,
		       inviter.id, inviter.username, inviter.display_name,
		       o.slug, o.display_name
		  FROM org_invitations i
		  JOIN users inviter ON inviter.id = i.inviter_user_id
		  JOIN orgs  o       ON o.id       = i.org_id
		 WHERE i.token_hash = $1
	`, tokenHash).Scan(&inv.ID, &inv.OrgID, &invitee, &inviteeEmail, &inv.Role, &inv.Status,
		&inv.ExpiresAt, &inv.CreatedAt, &acceptedAt,
		&inviterID, &inviterUsername, &inviterDisplay,
		&inv.OrgSlug, &inv.OrgDisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("invitation")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	inv.InviteeUserID = invitee
	if inviteeEmail != nil {
		inv.InviteeEmail = *inviteeEmail
	}
	inv.AcceptedAt = acceptedAt
	inv.Inviter = &model.UserRef{ID: inviterID, Username: inviterUsername, DisplayName: inviterDisplay}
	return &inv, nil
}

// AcceptInvitationParams holds inputs to AcceptInvitation.
type AcceptInvitationParams struct {
	TokenHash string
	UserID    uuid.UUID
	UserEmail string
}

// AcceptInvitation flips a pending invitation to accepted and inserts the
// org_members row in a single transaction.
//
// The caller must already be authenticated. We re-validate that the
// invitation belongs to the caller — either invitee_user_id matches, or
// invitee_email (case-insensitive) matches the user's email. Mismatches
// surface as Forbidden so we don't leak token validity to arbitrary signed-in
// users.
func (s *Store) AcceptInvitation(ctx context.Context, p AcceptInvitationParams) (*model.OrgInvitation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inv model.OrgInvitation
	var invitee *uuid.UUID
	var inviteeEmail *string
	err = tx.QueryRow(ctx, `
		SELECT id, org_id, invitee_user_id, invitee_email, role, status,
		       expires_at, created_at, accepted_at
		  FROM org_invitations
		 WHERE token_hash = $1
		 FOR UPDATE
	`, p.TokenHash).Scan(&inv.ID, &inv.OrgID, &invitee, &inviteeEmail, &inv.Role, &inv.Status,
		&inv.ExpiresAt, &inv.CreatedAt, &inv.AcceptedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("invitation")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	if inv.Status != "pending" {
		return nil, apperr.Conflict("invitation is no longer pending (status=" + inv.Status + ")")
	}
	if time.Now().UTC().After(inv.ExpiresAt) {
		// Opportunistically flip to expired so future GETs reflect reality.
		if _, err := tx.Exec(ctx,
			`UPDATE org_invitations SET status = 'expired' WHERE id = $1`, inv.ID); err != nil {
			return nil, apperr.Internal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, apperr.Internal(err)
		}
		return nil, apperr.Conflict("invitation has expired")
	}

	// Match check: invitee_user_id wins when set; otherwise compare email.
	if invitee != nil {
		if *invitee != p.UserID {
			return nil, apperr.Forbidden("invitation belongs to a different user")
		}
	} else if inviteeEmail != nil {
		if !strings.EqualFold(strings.TrimSpace(p.UserEmail), strings.TrimSpace(*inviteeEmail)) {
			return nil, apperr.Forbidden("invitation belongs to a different email address")
		}
	} else {
		// Defensive: the table CHECK should prevent both-null, but bail clearly.
		return nil, apperr.Internal(errors.New("invitation has neither invitee_user_id nor invitee_email"))
	}

	// Idempotent member insert. ON CONFLICT DO NOTHING handles the case where
	// the user is already a member at a different role — we don't change their
	// existing role since the invitation may grant a lower one than they hold.
	if _, err := tx.Exec(ctx, `
		INSERT INTO org_members (org_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO NOTHING
	`, inv.OrgID, p.UserID, inv.Role); err != nil {
		return nil, apperr.Internal(err)
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE org_invitations
		   SET status = 'accepted', accepted_at = $1, accepted_by = $2
		 WHERE id = $3
	`, now, p.UserID, inv.ID); err != nil {
		return nil, apperr.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	inv.Status = "accepted"
	inv.AcceptedAt = &now
	inv.InviteeUserID = invitee
	if inviteeEmail != nil {
		inv.InviteeEmail = *inviteeEmail
	}
	return &inv, nil
}

// RevokeInvitation marks a pending invitation as revoked. No-op if it's
// already in a terminal state.
func (s *Store) RevokeInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE org_invitations
		   SET status = 'revoked'
		 WHERE id = $1 AND org_id = $2 AND status = 'pending'
	`, invitationID, orgID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("pending invitation")
	}
	return nil
}

// isInvitableStoredRole accepts only the four roles that can be granted by
// invitation. Owner promotion goes through SetMemberRole.
func isInvitableStoredRole(r string) bool {
	switch r {
	case "maintainer", "developer", "reporter", "guest":
		return true
	}
	return false
}
