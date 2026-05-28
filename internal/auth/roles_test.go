package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoleLevel_Ordering(t *testing.T) {
	assert.Greater(t, RoleLevel(RoleOwner), RoleLevel(RoleMaintainer))
	assert.Greater(t, RoleLevel(RoleMaintainer), RoleLevel(RoleDeveloper))
	assert.Greater(t, RoleLevel(RoleDeveloper), RoleLevel(RoleReporter))
	assert.Greater(t, RoleLevel(RoleReporter), RoleLevel(RoleGuest))
	assert.Greater(t, RoleLevel(RoleGuest), 0)
	assert.Equal(t, 0, RoleLevel(""))
	assert.Equal(t, 0, RoleLevel("nope"))
}

func TestIsInvitableRole(t *testing.T) {
	assert.False(t, IsInvitableRole(RoleOwner), "owner cannot be invited")
	for _, r := range InvitableRoles {
		assert.True(t, IsInvitableRole(r), r)
	}
	assert.False(t, IsInvitableRole("admin"), "legacy name no longer accepted")
}

func TestCanWriteRepo(t *testing.T) {
	assert.True(t, CanWriteRepo(RoleOwner))
	assert.True(t, CanWriteRepo(RoleMaintainer))
	assert.True(t, CanWriteRepo(RoleDeveloper))
	assert.False(t, CanWriteRepo(RoleReporter))
	assert.False(t, CanWriteRepo(RoleGuest))
	assert.False(t, CanWriteRepo(""))
}

func TestCanModerateContent(t *testing.T) {
	assert.True(t, CanModerateContent(RoleOwner))
	assert.True(t, CanModerateContent(RoleMaintainer))
	assert.False(t, CanModerateContent(RoleDeveloper))
}

func TestCanManageMembers_And_CanAdminOrg(t *testing.T) {
	assert.True(t, CanManageMembers(RoleOwner))
	assert.True(t, CanManageMembers(RoleMaintainer))
	assert.False(t, CanManageMembers(RoleDeveloper))

	assert.True(t, CanAdminOrg(RoleOwner))
	assert.False(t, CanAdminOrg(RoleMaintainer))
}

func TestCanAssignRole(t *testing.T) {
	// Maintainers can grant developer/reporter/guest but not maintainer/owner.
	assert.True(t, CanAssignRole(RoleMaintainer, RoleDeveloper))
	assert.True(t, CanAssignRole(RoleMaintainer, RoleReporter))
	assert.False(t, CanAssignRole(RoleMaintainer, RoleMaintainer))
	assert.False(t, CanAssignRole(RoleMaintainer, RoleOwner))

	// Owners can grant anything, including owner (the second-owner path).
	assert.True(t, CanAssignRole(RoleOwner, RoleMaintainer))
	assert.True(t, CanAssignRole(RoleOwner, RoleOwner))

	// Developers can't manage members at all.
	assert.False(t, CanAssignRole(RoleDeveloper, RoleGuest))
}
