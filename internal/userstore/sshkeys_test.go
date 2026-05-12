package userstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

func seedUser(t *testing.T, store *userstore.Store, username string) (uuid string) {
	t.Helper()
	hash, err := auth.HashPassword("dontcare")
	require.NoError(t, err)
	u, _, err := store.CreateUser(context.Background(), userstore.CreateUserParams{
		Username:     username,
		Email:        username + "@example.test",
		PasswordHash: hash,
	})
	require.NoError(t, err)
	return u.ID.String()
}

func TestSSHKeys_CRUDAndResolve(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)
	ctx := context.Background()

	hash, err := auth.HashPassword("dontcare")
	require.NoError(t, err)
	u, _, err := store.CreateUser(ctx, userstore.CreateUserParams{
		Username: "sshtester", Email: "ssht@example.test", PasswordHash: hash,
	})
	require.NoError(t, err)

	// Create a key.
	key, err := store.CreateSSHKey(ctx, userstore.CreateSSHKeyParams{
		UserID:      u.ID,
		Title:       "laptop",
		Fingerprint: "SHA256:abc",
		PublicKey:   "ssh-ed25519 AAAA fake",
	})
	require.NoError(t, err)
	assert.Equal(t, "laptop", key.Title)
	assert.Equal(t, "SHA256:abc", key.Fingerprint)

	// Duplicate fingerprint rejected with Conflict.
	_, err = store.CreateSSHKey(ctx, userstore.CreateSSHKeyParams{
		UserID:      u.ID,
		Title:       "dup",
		Fingerprint: "SHA256:abc",
		PublicKey:   "ssh-ed25519 AAAA fake",
	})
	require.Error(t, err)
	ae := apperr.As(err)
	require.NotNil(t, ae)
	assert.Equal(t, apperr.CodeConflict, ae.Code)

	// Resolve maps fingerprint back to the owner.
	owner, err := store.ResolveSSHKeyByFingerprint(ctx, "SHA256:abc")
	require.NoError(t, err)
	assert.Equal(t, u.ID, owner.UserID)
	assert.Equal(t, "sshtester", owner.Username)

	// Resolve a missing fingerprint -> NotFound.
	_, err = store.ResolveSSHKeyByFingerprint(ctx, "SHA256:not-here")
	require.Error(t, err)
	ae = apperr.As(err)
	require.NotNil(t, ae)
	assert.Equal(t, apperr.CodeNotFound, ae.Code)

	// List shows the key.
	keys, err := store.ListSSHKeysForUser(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)

	// Cross-user delete is NotFound, not Forbidden.
	otherID := seedUser(t, store, "other")
	_ = otherID
	other, _, err := store.CreateUser(ctx, userstore.CreateUserParams{
		Username: "other2", Email: "o2@example.test", PasswordHash: hash,
	})
	require.NoError(t, err)
	err = store.DeleteSSHKey(ctx, other.ID, key.ID)
	require.Error(t, err)
	ae = apperr.As(err)
	require.NotNil(t, ae)
	assert.Equal(t, apperr.CodeNotFound, ae.Code)

	// Owner delete succeeds.
	require.NoError(t, store.DeleteSSHKey(ctx, u.ID, key.ID))
	keys, err = store.ListSSHKeysForUser(ctx, u.ID)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestSSHKeys_TouchUpdatesLastUsed(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	store := userstore.New(pool)
	ctx := context.Background()

	hash, err := auth.HashPassword("dontcare")
	require.NoError(t, err)
	u, _, err := store.CreateUser(ctx, userstore.CreateUserParams{
		Username: "toucher", Email: "t@example.test", PasswordHash: hash,
	})
	require.NoError(t, err)
	key, err := store.CreateSSHKey(ctx, userstore.CreateSSHKeyParams{
		UserID:      u.ID,
		Title:       "k",
		Fingerprint: "SHA256:t",
		PublicKey:   "ssh-ed25519 AAAA fake",
	})
	require.NoError(t, err)

	// Initially nil.
	keys, err := store.ListSSHKeysForUser(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Nil(t, keys[0].LastUsedAt)

	store.TouchSSHKey(ctx, key.ID)

	keys, err = store.ListSSHKeysForUser(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.NotNil(t, keys[0].LastUsedAt, "TouchSSHKey should stamp last_used_at")
}
