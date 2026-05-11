package userstore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// ----------------------------------------------------------------------------
// User SSH public keys
//
// The fingerprint column holds the canonical SHA256 form printed by
// ssh-keygen (e.g. "SHA256:..."). We require it to be globally unique because
// a key registered to two accounts is ambiguous at SSH auth time: the
// PublicKeyHandler can't decide which user is calling.
// ----------------------------------------------------------------------------

// CreateSSHKeyParams holds inputs to CreateSSHKey. Fingerprint is the
// canonical SHA256 form ("SHA256:..."); PublicKey is the OpenSSH
// authorized_keys text the user submitted.
type CreateSSHKeyParams struct {
	UserID      uuid.UUID
	Title       string
	Fingerprint string
	PublicKey   string
}

// CreateSSHKey inserts a key. Returns Conflict when a key with the same
// fingerprint is already registered for any user.
func (s *Store) CreateSSHKey(ctx context.Context, p CreateSSHKeyParams) (*model.SSHKey, error) {
	k := &model.SSHKey{
		ID:          uuid.New(),
		Title:       p.Title,
		Fingerprint: p.Fingerprint,
		PublicKey:   p.PublicKey,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_ssh_keys (id, user_id, title, fingerprint, public_key)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at
	`, k.ID, p.UserID, p.Title, p.Fingerprint, p.PublicKey).Scan(&k.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "ssh key")
	}
	return k, nil
}

// ListSSHKeysForUser returns all SSH keys belonging to the user.
func (s *Store) ListSSHKeysForUser(ctx context.Context, userID uuid.UUID) ([]model.SSHKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, fingerprint, public_key, created_at, last_used_at
		FROM user_ssh_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.SSHKey, 0)
	for rows.Next() {
		var k model.SSHKey
		if err := rows.Scan(&k.ID, &k.Title, &k.Fingerprint, &k.PublicKey, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// DeleteSSHKey removes a key belonging to userID. NotFound if the key
// doesn't exist or belongs to another user — we never leak whose key it is.
func (s *Store) DeleteSSHKey(ctx context.Context, userID, keyID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM user_ssh_keys WHERE id = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("ssh key")
	}
	return nil
}

// SSHKeyOwner is the lookup result returned to the sshd's auth handler.
type SSHKeyOwner struct {
	KeyID    uuid.UUID
	UserID   uuid.UUID
	Username string
}

// ResolveSSHKeyByFingerprint maps a key fingerprint to its owning user. Used
// by the sshd PublicKeyHandler to decide whose identity to attach to a
// session. Returns NotFound when no key matches.
func (s *Store) ResolveSSHKeyByFingerprint(ctx context.Context, fingerprint string) (*SSHKeyOwner, error) {
	var o SSHKeyOwner
	err := s.pool.QueryRow(ctx, `
		SELECT k.id, u.id, u.username
		FROM user_ssh_keys k
		JOIN users u ON u.id = k.user_id
		WHERE k.fingerprint = $1 AND u.is_active
	`, fingerprint).Scan(&o.KeyID, &o.UserID, &o.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("ssh key")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &o, nil
}

// TouchSSHKey updates last_used_at to now. Best-effort; mirrors TouchPAT.
func (s *Store) TouchSSHKey(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE user_ssh_keys SET last_used_at = $1 WHERE id = $2`,
		time.Now().UTC(), id)
}
