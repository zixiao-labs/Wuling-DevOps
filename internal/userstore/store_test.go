package userstore_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// store opens the test pool, resets all user-data tables, and returns a fresh
// *userstore.Store. Each test gets a clean slate.
func store(t *testing.T) (*userstore.Store, *db.Pool) {
	t.Helper()
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	return userstore.New(pool), pool
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := auth.HashPassword(pw)
	require.NoError(t, err)
	return h
}

func uniqueUser(prefix string) (username, email string) {
	u := prefix + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	return u, u + "@example.com"
}

// ---------- Users ----------

func TestCreateUser_HappyPath(t *testing.T) {
	s, pool := store(t)
	ctx := context.Background()

	username, email := uniqueUser("alice")
	hash := mustHash(t, "pw1234567")

	user, org, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: hash,
	})
	require.NoError(t, err)

	assert.Equal(t, username, user.Username)
	assert.Equal(t, email, user.Email)
	assert.True(t, user.IsActive)
	assert.False(t, user.IsAdmin)

	assert.True(t, org.IsPersonal)
	assert.Equal(t, strings.ToLower(username), org.Slug)

	// Membership row exists with role=owner.
	role, err := s.MemberRole(ctx, org.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "owner", role)

	// Password hash persisted.
	var stored *string
	err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, user.ID).Scan(&stored)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, hash, *stored)
}

func TestCreateUser_UsernameCollision(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	username, email := uniqueUser("bob")
	_, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	// Different case + different email — username uniqueness is LOWER()-indexed.
	_, _, err = s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     strings.ToUpper(username),
		Email:        "other-" + email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)
}

func TestCreateUser_EmailCollision(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	usernameA, email := uniqueUser("carol")
	usernameB, _ := uniqueUser("dave")
	_, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     usernameA,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	_, _, err = s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     usernameB,
		Email:        strings.ToUpper(email), // case-insensitive uniqueness
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)
}

func TestCreateUser_ParallelRace(t *testing.T) {
	s, pool := store(t)
	ctx := context.Background()

	username, email := uniqueUser("racer")
	hash := mustHash(t, "pw1234567")

	const N = 16
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ok      int
		conflicts int
		other   int
	)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
				Username:     username,
				Email:        email,
				PasswordHash: hash,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case apperr.As(err) != nil && apperr.As(err).Code == apperr.CodeConflict:
				conflicts++
			default:
				other++
				t.Logf("unexpected race error: %v", err)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, ok, "exactly one goroutine must succeed")
	assert.Equal(t, N-1, conflicts, "all losers must surface as Conflict")
	assert.Equal(t, 0, other, "no other error categories allowed")

	// And exactly one row in users — the rolled-back transactions must not leak
	// orphaned partial inserts.
	var count int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE LOWER(username) = LOWER($1)`, username).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "no leaked rows from rolled-back transactions")
}

func TestGetUserByLogin_PrefersUsernameOnAmbiguity(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	// User A: username = "bob"
	_, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     "bob",
		Email:        "bob-username@example.com",
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	// User B: email starts with "bob"
	_, _, err = s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     "barbara",
		Email:        "bob@example.com",
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	got, _, err := s.GetUserByLogin(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, "bob", got.Username, "ambiguous lookup must prefer username match (per ORDER BY in store.go)")
}

func TestGetUserByLogin_OAuthOnly_NoPassword(t *testing.T) {
	_, pool := store(t)
	ctx := context.Background()

	// Bypass CreateUser so we can leave password_hash NULL — production code
	// will create such rows from the OAuth callback.
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, email, display_name, password_hash, approval_status)
		VALUES ($1, 'oauthonly', 'oauth@example.com', 'OAuth Only', NULL, 'approved')
	`, id)
	require.NoError(t, err)

	s := userstore.New(pool)
	got, hash, err := s.GetUserByLogin(ctx, "oauthonly")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "oauthonly", got.Username)
	assert.Empty(t, hash, "OAuth-only row must surface an empty hash so the handler can prompt for GitHub login")
}

func TestPasswordHashFor_Inactive(t *testing.T) {
	_, pool := store(t)
	ctx := context.Background()
	s := userstore.New(pool)

	username, email := uniqueUser("inactive")
	user, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, user.ID)
	require.NoError(t, err)

	_, _, err = s.PasswordHashFor(ctx, username)
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeNotFound, e.Code, "inactive users surface as NotFound (current behavior, not Unauthorized)")
}

// ---------- Orgs / membership ----------

func TestCreateOrg_GrantsOwner(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	username, email := uniqueUser("orgowner")
	user, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	org, err := s.CreateOrg(ctx, userstore.CreateOrgParams{
		Slug:        "team-" + username,
		DisplayName: "Team " + username,
		OwnerUserID: user.ID,
	})
	require.NoError(t, err)
	assert.False(t, org.IsPersonal)

	role, err := s.MemberRole(ctx, org.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "owner", role)
}

func TestMemberRole_NonMember(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	username, email := uniqueUser("a")
	user, org, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	// Some random other user id.
	role, err := s.MemberRole(ctx, org.ID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "", role)

	// And the owning user is still a member.
	role, err = s.MemberRole(ctx, org.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "owner", role)
}

func TestListOrgsForUser_PersonalFirst(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	username, email := uniqueUser("multi")
	user, personal, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	team, err := s.CreateOrg(ctx, userstore.CreateOrgParams{
		Slug:        "z-team-" + username, // alphabetically after personal
		OwnerUserID: user.ID,
	})
	require.NoError(t, err)

	orgs, err := s.ListOrgsForUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, orgs, 2)
	assert.Equal(t, personal.ID, orgs[0].ID, "personal org sorts first regardless of slug")
	assert.Equal(t, team.ID, orgs[1].ID)
}

func TestCreateOrg_SlugCollision(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	username, email := uniqueUser("coll")
	user, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	_, err = s.CreateOrg(ctx, userstore.CreateOrgParams{
		Slug:        "shared-slug",
		OwnerUserID: user.ID,
	})
	require.NoError(t, err)

	_, err = s.CreateOrg(ctx, userstore.CreateOrgParams{
		Slug:        "Shared-Slug", // case-insensitive uniqueness
		OwnerUserID: user.ID,
	})
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeConflict, e.Code)
}

// ---------- Repos / path resolution ----------

func seedProject(t *testing.T, s *userstore.Store) (orgID, projectID uuid.UUID, orgSlug, projSlug string) {
	t.Helper()
	ctx := context.Background()
	username, email := uniqueUser("repo")
	user, org, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)
	_ = user
	proj, err := s.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID,
		Slug:  "p-" + username,
	})
	require.NoError(t, err)
	return org.ID, proj.ID, org.Slug, proj.Slug
}

func TestCreateRepo_DefaultsApplied(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	_, projID, _, _ := seedProject(t, s)

	repo, err := s.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID: projID,
		Slug:      "myrepo",
	})
	require.NoError(t, err)
	assert.Equal(t, "main", repo.DefaultBranch)
	assert.Equal(t, "private", repo.Visibility)
	assert.True(t, repo.IsEmpty)
	assert.Equal(t, "myrepo", repo.Slug)
}

func TestResolveRepoPath_HappyPath(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	orgID, projID, orgSlug, projSlug := seedProject(t, s)

	repo, err := s.CreateRepo(ctx, userstore.CreateRepoParams{ProjectID: projID, Slug: "r"})
	require.NoError(t, err)

	got, gotProj, gotOrg, err := s.ResolveRepoPath(ctx, orgSlug, projSlug, "r")
	require.NoError(t, err)
	assert.Equal(t, repo.ID, got.ID)
	assert.Equal(t, projID, gotProj)
	assert.Equal(t, orgID, gotOrg)
}

func TestResolveRepoPath_CaseInsensitive(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	_, projID, orgSlug, projSlug := seedProject(t, s)
	_, err := s.CreateRepo(ctx, userstore.CreateRepoParams{ProjectID: projID, Slug: "lowercase"})
	require.NoError(t, err)

	_, _, _, err = s.ResolveRepoPath(ctx, strings.ToUpper(orgSlug), strings.ToUpper(projSlug), "LOWERCASE")
	require.NoError(t, err)
}

func TestResolveRepoPath_NotFound(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	_, projID, orgSlug, projSlug := seedProject(t, s)
	_, err := s.CreateRepo(ctx, userstore.CreateRepoParams{ProjectID: projID, Slug: "exists"})
	require.NoError(t, err)

	cases := []struct {
		name                          string
		org, proj, repo string
	}{
		{"missing org", "no-such-org", projSlug, "exists"},
		{"missing project", orgSlug, "no-such-proj", "exists"},
		{"missing repo", orgSlug, projSlug, "no-such-repo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, err := s.ResolveRepoPath(ctx, c.org, c.proj, c.repo)
			require.Error(t, err)
			e := apperr.As(err)
			require.NotNil(t, e)
			assert.Equal(t, apperr.CodeNotFound, e.Code)
		})
	}
}

func TestUpdateRepoSize_Negative_Validation(t *testing.T) {
	s, pool := store(t)
	ctx := context.Background()
	_, projID, _, _ := seedProject(t, s)
	repo, err := s.CreateRepo(ctx, userstore.CreateRepoParams{ProjectID: projID, Slug: "sz"})
	require.NoError(t, err)

	err = s.UpdateRepoSize(ctx, repo.ID, -1)
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeValidation, e.Code)

	// And the row's size is unchanged.
	var got int64
	err = pool.QueryRow(ctx, `SELECT size_bytes FROM repos WHERE id = $1`, repo.ID).Scan(&got)
	require.NoError(t, err)
	assert.Equal(t, int64(0), got, "validation failure must not have written")
}

func TestDeleteRepo_NotFound(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	err := s.DeleteRepo(ctx, uuid.New())
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeNotFound, e.Code)
}

func TestMarkRepoNotEmpty_Idempotent(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	_, projID, _, _ := seedProject(t, s)
	repo, err := s.CreateRepo(ctx, userstore.CreateRepoParams{ProjectID: projID, Slug: "idem"})
	require.NoError(t, err)

	require.NoError(t, s.MarkRepoNotEmpty(ctx, repo.ID))
	require.NoError(t, s.MarkRepoNotEmpty(ctx, repo.ID))
}

// ---------- PATs ----------

func TestCreatePAT_ReturnedView(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	username, email := uniqueUser("pat")
	user, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	exp := time.Now().Add(time.Hour).UTC()
	v, err := s.CreatePAT(ctx, userstore.CreatePATParams{
		UserID:    user.ID,
		Name:      "ci",
		Hash:      mustHash(t, auth.AccessTokenPrefix+"abc"),
		Scopes:    []string{"repo:read", "repo:write"},
		ExpiresAt: &exp,
	})
	require.NoError(t, err)
	assert.Equal(t, "ci", v.Name)
	assert.Equal(t, []string{"repo:read", "repo:write"}, v.Scopes)
	require.NotNil(t, v.ExpiresAt)
	assert.WithinDuration(t, exp, *v.ExpiresAt, time.Second)
}

func TestListPATAuthRowsForUser_ScopesRoundTrip(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	username, email := uniqueUser("listpat")
	user, _, err := s.CreateUser(ctx, userstore.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: mustHash(t, "pw1234567"),
	})
	require.NoError(t, err)

	scopes := []string{"repo:read", "repo:write"}
	_, err = s.CreatePAT(ctx, userstore.CreatePATParams{
		UserID: user.ID,
		Name:   "n",
		Hash:   mustHash(t, "secret"),
		Scopes: scopes,
	})
	require.NoError(t, err)

	rows, err := s.ListPATAuthRowsForUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, scopes, rows[0].Scopes)
}

func TestDeletePAT_OtherUser_NotFound(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()

	uA, eA := uniqueUser("a")
	userA, _, err := s.CreateUser(ctx, userstore.CreateUserParams{Username: uA, Email: eA, PasswordHash: mustHash(t, "pw1234567")})
	require.NoError(t, err)

	uB, eB := uniqueUser("b")
	userB, _, err := s.CreateUser(ctx, userstore.CreateUserParams{Username: uB, Email: eB, PasswordHash: mustHash(t, "pw1234567")})
	require.NoError(t, err)

	patA, err := s.CreatePAT(ctx, userstore.CreatePATParams{
		UserID: userA.ID,
		Name:   "a-pat",
		Hash:   mustHash(t, "x"),
		Scopes: []string{"repo:read"},
	})
	require.NoError(t, err)

	// User B tries to delete user A's PAT — must fail with NotFound (not Forbidden,
	// to avoid leaking which IDs exist).
	err = s.DeletePAT(ctx, userB.ID, patA.ID)
	require.Error(t, err)
	e := apperr.As(err)
	require.NotNil(t, e)
	assert.Equal(t, apperr.CodeNotFound, e.Code)

	// And the PAT still exists for user A.
	rows, err := s.ListPATAuthRowsForUser(ctx, userA.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "cross-user delete must not have removed the PAT")
}

func TestTouchPAT_NoErrorOnMissing(t *testing.T) {
	s, _ := store(t)
	ctx := context.Background()
	// Should not panic or fail.
	s.TouchPAT(ctx, uuid.New())
}
