package auth

// Org-membership roles, ordered Owner > Maintainer > Developer > Reporter > Guest.
//
// GitLab's tier names are intentional — operators familiar with GitLab can
// guess the semantics without reading code. The mapping from the original
// {owner, admin, member} scheme is:
//
//   - admin  -> maintainer  (could create projects, never could delete org)
//   - member -> developer   (could push to repos)
//
// Use the Can* helpers below rather than `==` comparisons in handlers — the
// helpers double as the single place to look up "what does each role allow"
// when stage 2 introduces finer-grained gates.
const (
	RoleOwner      = "owner"
	RoleMaintainer = "maintainer"
	RoleDeveloper  = "developer"
	RoleReporter   = "reporter"
	RoleGuest      = "guest"
)

// AllRoles is the ordered set of legal role values, useful for validation
// and admin UIs that render a role dropdown.
var AllRoles = []string{RoleOwner, RoleMaintainer, RoleDeveloper, RoleReporter, RoleGuest}

// InvitableRoles is the subset that can be granted via the invitation flow.
// Owner is intentionally excluded — owners must be promoted explicitly by an
// existing owner using the role-change endpoint, never via a link that could
// leak.
var InvitableRoles = []string{RoleMaintainer, RoleDeveloper, RoleReporter, RoleGuest}

// RoleLevel returns an integer rank for ordering comparisons.
// 0 means "not a member" (empty string).
func RoleLevel(r string) int {
	switch r {
	case RoleOwner:
		return 50
	case RoleMaintainer:
		return 40
	case RoleDeveloper:
		return 30
	case RoleReporter:
		return 20
	case RoleGuest:
		return 10
	}
	return 0
}

// IsValidRole reports whether r is one of the five legal role strings.
func IsValidRole(r string) bool { return RoleLevel(r) > 0 }

// IsInvitableRole reports whether r is a role that can be sent in a magic-link
// invitation (anything except owner).
func IsInvitableRole(r string) bool {
	return r != RoleOwner && IsValidRole(r)
}

// CanReadOrg reports whether the role can see private org content (members
// list, project list, internal repo metadata). Any membership grants read.
// Public repos are readable by non-members via the visibility check, not
// here.
func CanReadOrg(r string) bool { return RoleLevel(r) >= RoleLevel(RoleGuest) }

// CanReadRepo is the standard read gate for non-public repos. Identical to
// CanReadOrg today; broken out so future "guest can only read public" tweaks
// land in one place.
func CanReadRepo(r string) bool { return RoleLevel(r) >= RoleLevel(RoleGuest) }

// CanWriteRepo gates push, branch create, MR open, label CRUD, and other
// content mutations. Reporter and guest are read-only.
func CanWriteRepo(r string) bool { return RoleLevel(r) >= RoleLevel(RoleDeveloper) }

// CanModerateContent gates "edit somebody else's issue/MR/comment" actions.
// Authors retain edit rights on their own content regardless of role —
// handlers OR this with an author check.
func CanModerateContent(r string) bool { return RoleLevel(r) >= RoleLevel(RoleMaintainer) }

// CanCreateProject gates creating new projects under an org.
func CanCreateProject(r string) bool { return RoleLevel(r) >= RoleLevel(RoleMaintainer) }

// CanManageMembers gates listing/adding/removing org members and managing
// invitations.
func CanManageMembers(r string) bool { return RoleLevel(r) >= RoleLevel(RoleMaintainer) }

// CanAdminOrg gates owner-only actions: changing role to or from owner,
// deleting the org, transferring ownership.
func CanAdminOrg(r string) bool { return r == RoleOwner }

// CanAssignRole reports whether an actor with role `actorRole` may grant
// targetRole to another user. The rule is: you can never grant a role above
// or equal to your own (except owner-to-owner promotion, which goes through
// a different code path that requires confirmation). Maintainers can manage
// developers/reporters/guests but not other maintainers.
func CanAssignRole(actorRole, targetRole string) bool {
	if !CanManageMembers(actorRole) {
		return false
	}
	if targetRole == RoleOwner {
		return actorRole == RoleOwner
	}
	return RoleLevel(actorRole) > RoleLevel(targetRole)
}
