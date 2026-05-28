// Members store helpers: org_members CRUD with last-owner guards.
//
// All mutations that could remove the final owner of an org run inside a
// transaction with row-locked SELECTs, mirroring the last-admin guard in
// UpdateUser. We never want to leave an org without a route back to owner
// access — the only legitimate way out is `org_members.user_id`'s cascade
// when the user account itself is deleted, in which case the org will be
// adopted by a system process or left dangling.

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

// AddMemberParams holds inputs to AddMember.
type AddMemberParams struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
	Role   string
}

// AddMember inserts a new (org_id, user_id) row with the given role. Returns
// Conflict when the user is already a member. Used by the accept-invitation
// path; ordinary admins go through the invitation flow.
func (s *Store) AddMember(ctx context.Context, p AddMemberParams) error {
	if !isStoredRole(p.Role) {
		return apperr.Validation("invalid role", map[string]any{"role": "must be one of owner/maintainer/developer/reporter/guest"})
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)`,
		p.OrgID, p.UserID, p.Role)
	if err != nil {
		return mapInsertErr(err, "org member")
	}
	return nil
}

// ListMembers returns every member of an org, joined onto the user row so
// callers can render rich listings (avatar, display name, email). Sort is
// owners-first then alphabetical by username — the same ordering GitLab uses
// for the members page.
func (s *Store) ListMembers(ctx context.Context, orgID uuid.UUID) ([]model.OrgMember, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.username, u.display_name, u.email, u.avatar_updated_at, m.role, m.created_at
		  FROM org_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.org_id = $1
		 ORDER BY
		   CASE m.role
		     WHEN 'owner'      THEN 0
		     WHEN 'maintainer' THEN 1
		     WHEN 'developer'  THEN 2
		     WHEN 'reporter'   THEN 3
		     ELSE                   4
		   END,
		   LOWER(u.username) ASC
	`, orgID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := []model.OrgMember{}
	for rows.Next() {
		var m model.OrgMember
		var avatarUpdatedAt *time.Time
		if err := rows.Scan(&m.UserID, &m.Username, &m.DisplayName, &m.Email,
			&avatarUpdatedAt, &m.Role, &m.JoinedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		m.AvatarURL = AvatarURL(m.Username, avatarUpdatedAt)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// SetMemberRole updates one membership row's role. Returns Conflict when the
// change would zero out the owners of the org (demoting the last owner).
//
// The actor-permission check (i.e. "is the caller allowed to grant this role
// at all?") lives at the HTTP layer — the store only enforces structural
// invariants.
func (s *Store) SetMemberRole(ctx context.Context, orgID, userID uuid.UUID, newRole string) error {
	if !isStoredRole(newRole) {
		return apperr.Validation("invalid role", map[string]any{"role": "must be one of owner/maintainer/developer/reporter/guest"})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var curRole string
	err = tx.QueryRow(ctx, `
		SELECT role FROM org_members
		 WHERE org_id = $1 AND user_id = $2
		 FOR UPDATE
	`, orgID, userID).Scan(&curRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("member")
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if curRole == newRole {
		return tx.Commit(ctx)
	}

	// If we're moving the row out of 'owner', verify there's at least one
	// remaining owner under row locks so a concurrent demote of a different
	// owner can't race us into zero.
	if curRole == "owner" {
		if err := assertOtherOwnerExistsTx(ctx, tx, orgID, userID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE org_members SET role = $1 WHERE org_id = $2 AND user_id = $3`,
		newRole, orgID, userID); err != nil {
		return apperr.Internal(err)
	}
	return tx.Commit(ctx)
}

// RemoveMember deletes one membership row. Like SetMemberRole it refuses to
// remove the final owner.
func (s *Store) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var curRole string
	err = tx.QueryRow(ctx, `
		SELECT role FROM org_members
		 WHERE org_id = $1 AND user_id = $2
		 FOR UPDATE
	`, orgID, userID).Scan(&curRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("member")
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if curRole == "owner" {
		if err := assertOtherOwnerExistsTx(ctx, tx, orgID, userID); err != nil {
			return err
		}
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM org_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("member")
	}
	return tx.Commit(ctx)
}

// FindUserByUsernameOrEmail resolves the input to a user row, accepting either
// a username or an email address. Used by the invite-by-identifier path.
// Returns NotFound (not an error) if nothing matches.
func (s *Store) FindUserByUsernameOrEmail(ctx context.Context, input string) (*model.User, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, apperr.Validation("identifier required", nil)
	}
	if strings.Contains(input, "@") {
		return s.GetUserByEmail(ctx, input)
	}
	return s.GetUserByUsername(ctx, input)
}

// assertOtherOwnerExistsTx refuses the surrounding transaction if userID is
// the only 'owner' in the org. Runs under FOR UPDATE so a concurrent
// demote/remove of a sibling owner is serialised — the second tx will
// re-evaluate against the freshly committed state and refuse if it would
// zero out owners.
func assertOtherOwnerExistsTx(ctx context.Context, tx pgx.Tx, orgID, userID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT user_id FROM org_members
		 WHERE org_id = $1 AND role = 'owner'
		 FOR UPDATE
	`, orgID)
	if err != nil {
		return apperr.Internal(err)
	}
	defer rows.Close()
	others := 0
	for rows.Next() {
		var other uuid.UUID
		if err := rows.Scan(&other); err != nil {
			return apperr.Internal(err)
		}
		if other != userID {
			others++
		}
	}
	if err := rows.Err(); err != nil {
		return apperr.Internal(err)
	}
	if others == 0 {
		return apperr.Conflict("refusing to demote or remove the last org owner")
	}
	return nil
}

// isStoredRole mirrors the CHECK constraint on org_members.role. Lives here
// (not in auth/) so the store stays self-sufficient.
func isStoredRole(r string) bool {
	switch r {
	case "owner", "maintainer", "developer", "reporter", "guest":
		return true
	}
	return false
}
