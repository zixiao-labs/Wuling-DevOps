package userstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// makeMembershipFixture creates a non-personal org owned by `owner`, then
// returns (org, owner, extra members). Each extra member is added at the
// requested role.
func makeMembershipFixture(t *testing.T, s *userstore.Store, roles ...string) (*ownedOrg, []memberInfo) {
	t.Helper()
	ctx := context.Background()

	ownerName, ownerEmail := uniqueUser("owner")
	owner, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     ownerName,
		Email:        ownerEmail,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)
	org, err := s.CreateOrg(ctx, userstore.CreateOrgParams{
		Slug:        "team-" + uuid.NewString()[:8],
		DisplayName: "Team Fixture",
		OwnerUserID: owner.ID,
	})
	require.NoError(t, err)

	infos := make([]memberInfo, 0, len(roles))
	for _, r := range roles {
		uname, email := uniqueUser("m")
		u, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
			Username:     uname,
			Email:        email,
			PasswordHash: mustHash(t, "pw1234567"),
		})
		require.NoError(t, err)
		require.NoError(t, s.AddMember(ctx, userstore.AddMemberParams{
			OrgID:  org.ID,
			UserID: u.ID,
			Role:   r,
		}))
		infos = append(infos, memberInfo{UserID: u.ID, Username: u.Username, Email: u.Email})
	}

	return &ownedOrg{ID: org.ID, OwnerID: owner.ID}, infos
}

type ownedOrg struct {
	ID      uuid.UUID
	OwnerID uuid.UUID
}

type memberInfo struct {
	UserID   uuid.UUID
	Username string
	Email    string
}

// ---------- members ----------

func TestListMembers_OwnerThenAlpha(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, _ := makeMembershipFixture(t, s, "developer", "maintainer", "guest")

	members, err := s.ListMembers(ctx, org.ID)
	require.NoError(t, err)
	require.Len(t, members, 4)
	// First row is the owner; order falls out of the role-rank ORDER BY.
	assert.Equal(t, "owner", members[0].Role)
	assert.Equal(t, "maintainer", members[1].Role)
	// The two member-rank rows come in alphabetical order by username.
	assert.True(t, members[2].Username < members[3].Username,
		"expected alphabetical order within same rank")
}

func TestSetMemberRole_PromoteAndDemote(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, members := makeMembershipFixture(t, s, "developer")
	dev := members[0]

	require.NoError(t, s.SetMemberRole(ctx, org.ID, dev.UserID, "maintainer"))
	r, _ := s.MemberRole(ctx, org.ID, dev.UserID)
	assert.Equal(t, "maintainer", r)

	require.NoError(t, s.SetMemberRole(ctx, org.ID, dev.UserID, "guest"))
	r, _ = s.MemberRole(ctx, org.ID, dev.UserID)
	assert.Equal(t, "guest", r)
}

func TestSetMemberRole_LastOwnerRefused(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, _ := makeMembershipFixture(t, s, "developer") // only one owner
	err := s.SetMemberRole(ctx, org.ID, org.OwnerID, "developer")
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)
}

func TestSetMemberRole_DemoteOwner_OK_WhenSecondOwnerExists(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, members := makeMembershipFixture(t, s, "developer")
	dev := members[0]
	// Promote the dev to owner first.
	require.NoError(t, s.SetMemberRole(ctx, org.ID, dev.UserID, "owner"))
	// Now demoting the original owner must succeed.
	require.NoError(t, s.SetMemberRole(ctx, org.ID, org.OwnerID, "maintainer"))
}

func TestRemoveMember_LastOwnerRefused(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, _ := makeMembershipFixture(t, s, "developer")
	err := s.RemoveMember(ctx, org.ID, org.OwnerID)
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)
}

func TestRemoveMember_OrdinaryMember_OK(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, members := makeMembershipFixture(t, s, "developer")
	dev := members[0]
	require.NoError(t, s.RemoveMember(ctx, org.ID, dev.UserID))
	r, _ := s.MemberRole(ctx, org.ID, dev.UserID)
	assert.Equal(t, "", r)
}

func TestAddMember_InvalidRoleRejected(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, _ := makeMembershipFixture(t, s)
	uname, email := uniqueUser("nope")
	u, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: uname, Email: email, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)
	err = s.AddMember(ctx, userstore.AddMemberParams{
		OrgID: org.ID, UserID: u.ID, Role: "wrong",
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeValidation, e.Code)
}

// ---------- invitations ----------

func TestCreateInvitation_ByUserID(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	org, _ := makeMembershipFixture(t, s)
	invName, invEmail := uniqueUser("invitee")
	invitee, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: invName, Email: invEmail, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	inv, err := s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeUserID: &invitee.ID,
		Role:          "developer",
		TokenHash:     "deadbeef",
		TTL:           48 * time.Hour,
	})
	require.NoError(t, err)
	require.NotNil(t, inv)
	assert.Equal(t, "pending", inv.Status)
	assert.Equal(t, "developer", inv.Role)
	assert.WithinDuration(t, time.Now().Add(48*time.Hour), inv.ExpiresAt, 5*time.Second)
}

func TestCreateInvitation_RejectsOwnerRole(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	org, _ := makeMembershipFixture(t, s)

	_, err := s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeEmail:  "x@example.com",
		Role:          "owner",
		TokenHash:     "deadbeef",
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeValidation, e.Code)
}

func TestCreateInvitation_DuplicatePendingConflict(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	org, _ := makeMembershipFixture(t, s)

	_, err := s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeEmail:  "dup@example.com",
		Role:          "developer",
		TokenHash:     "hash-a",
	})
	require.NoError(t, err)

	_, err = s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeEmail:  "DUP@example.com", // case differs but the index is LOWER()'d
		Role:          "developer",
		TokenHash:     "hash-b",
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)
}

func TestAcceptInvitation_HappyPath(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	org, _ := makeMembershipFixture(t, s)

	invName, invEmail := uniqueUser("accept")
	invitee, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: invName, Email: invEmail, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	created, err := s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeUserID: &invitee.ID,
		Role:          "reporter",
		TokenHash:     "tok-1",
	})
	require.NoError(t, err)
	_ = created

	got, err := s.AcceptInvitation(ctx, userstore.AcceptInvitationParams{
		TokenHash: "tok-1",
		UserID:    invitee.ID,
		UserEmail: invitee.Email,
	})
	require.NoError(t, err)
	assert.Equal(t, "accepted", got.Status)

	role, err := s.MemberRole(ctx, org.ID, invitee.ID)
	require.NoError(t, err)
	assert.Equal(t, "reporter", role)
}

func TestAcceptInvitation_WrongUserForbidden(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	org, _ := makeMembershipFixture(t, s)

	intendedName, intendedEmail := uniqueUser("intended")
	intended, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: intendedName, Email: intendedEmail, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)
	wrongName, wrongEmail := uniqueUser("wrong")
	wrong, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: wrongName, Email: wrongEmail, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	_, err = s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeUserID: &intended.ID,
		Role:          "developer",
		TokenHash:     "tok-mismatch",
	})
	require.NoError(t, err)

	_, err = s.AcceptInvitation(ctx, userstore.AcceptInvitationParams{
		TokenHash: "tok-mismatch",
		UserID:    wrong.ID,
		UserEmail: wrong.Email,
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeForbidden, e.Code)
}

func TestAcceptInvitation_RejectedAfterRevoke(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	org, _ := makeMembershipFixture(t, s)

	invName, invEmail := uniqueUser("rev")
	invitee, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: invName, Email: invEmail, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)
	inv, err := s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeUserID: &invitee.ID,
		Role:          "developer",
		TokenHash:     "tok-rev",
	})
	require.NoError(t, err)

	require.NoError(t, s.RevokeInvitation(ctx, org.ID, inv.ID))

	_, err = s.AcceptInvitation(ctx, userstore.AcceptInvitationParams{
		TokenHash: "tok-rev",
		UserID:    invitee.ID,
		UserEmail: invitee.Email,
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)
}

func TestAcceptInvitation_ExpiredFlipsStatus(t *testing.T) {
	s, pool := store(t)
	ctx := context.Background()
	org, _ := makeMembershipFixture(t, s)

	invName, invEmail := uniqueUser("exp")
	invitee, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: invName, Email: invEmail, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)
	inv, err := s.CreateInvitation(ctx, userstore.CreateInvitationParams{
		OrgID:         org.ID,
		InviterUserID: org.OwnerID,
		InviteeUserID: &invitee.ID,
		Role:          "developer",
		TokenHash:     "tok-exp",
	})
	require.NoError(t, err)

	// Force expires_at into the past.
	_, err = pool.Exec(ctx,
		`UPDATE org_invitations SET expires_at = now() - interval '1 hour' WHERE id = $1`,
		inv.ID)
	require.NoError(t, err)

	_, err = s.AcceptInvitation(ctx, userstore.AcceptInvitationParams{
		TokenHash: "tok-exp",
		UserID:    invitee.ID,
		UserEmail: invitee.Email,
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)

	// Side effect: status flipped to 'expired' so a re-accept returns the
	// same conflict for a clearer reason.
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM org_invitations WHERE id = $1`, inv.ID).Scan(&status))
	assert.Equal(t, "expired", status)
}

// ---------- avatar ----------

func TestMarkAvatarUploaded_RoundTrip(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	uname, email := uniqueUser("av")
	u, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username: uname, Email: email, PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)
	assert.Equal(t, "", u.AvatarURL, "no avatar yet")

	ts, err := s.MarkAvatarUploaded(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, ts.IsZero())

	again, err := s.GetUserByID(ctx, u.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, again.AvatarURL)

	require.NoError(t, s.ClearAvatar(ctx, u.ID))
	cleared, err := s.GetUserByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Empty(t, cleared.AvatarURL)
}
