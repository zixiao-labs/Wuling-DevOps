package userstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// AvatarURL is the canonical public URL for a user's uploaded avatar. It
// returns the empty string when the user has not uploaded one — frontends
// then fall back to a deterministic initials tile. The query-string version
// is the Unix timestamp of the last upload, used purely as a cache buster so
// the browser refetches the moment the user changes their avatar.
//
// Lives in the store package because every code path that builds a *model.User
// from a DB row needs to call this — keeping it here avoids importing model
// into auth/ just for one constant.
func AvatarURL(username string, updatedAt *time.Time) string {
	if updatedAt == nil || username == "" {
		return ""
	}
	return fmt.Sprintf("/api/v1/users/%s/avatar?v=%d", username, updatedAt.Unix())
}

// MarkAvatarUploaded sets avatar_updated_at = now(). Called by the avatar
// upload handler after the resized PNG has been written to disk.
//
// The returned time is the value persisted on the row; callers cache-bust
// against this value rather than computing their own.
func (s *Store) MarkAvatarUploaded(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	var ts time.Time
	err := s.pool.QueryRow(ctx, `
		UPDATE users SET avatar_updated_at = now(), updated_at = now()
		 WHERE id = $1
		 RETURNING avatar_updated_at
	`, userID).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, apperr.NotFound("user")
	}
	if err != nil {
		return time.Time{}, apperr.Internal(err)
	}
	return ts, nil
}

// ClearAvatar unsets avatar_updated_at. The caller is responsible for
// removing the file on disk; the DB op and the FS op are intentionally
// independent so a half-failed delete is recoverable by re-running.
func (s *Store) ClearAvatar(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET avatar_updated_at = NULL, updated_at = now()
		 WHERE id = $1
	`, userID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("user")
	}
	return nil
}

// AvatarUpdatedAt returns the avatar_updated_at value for the user, or NotFound
// if the user does not exist. Used by the public avatar endpoint to set the
// Last-Modified / ETag headers without a full GetUserByUsername round-trip.
func (s *Store) AvatarUpdatedAt(ctx context.Context, username string) (*time.Time, uuid.UUID, error) {
	var userID uuid.UUID
	var ts *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, avatar_updated_at FROM users WHERE LOWER(username) = LOWER($1)`,
		username).Scan(&userID, &ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uuid.Nil, apperr.NotFound("user")
	}
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	return ts, userID, nil
}
