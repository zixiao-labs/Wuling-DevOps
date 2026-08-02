package githubwebhook

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zixiao-labs/wuling-devops/internal/db"
)

// Store persists webhook delivery IDs for idempotent redelivery handling.
type Store struct {
	Pool *db.Pool
}

// ClaimDelivery inserts delivery_id. Returns (true, nil) on first sight,
// (false, nil) when the delivery was already processed (unique violation).
func (s *Store) ClaimDelivery(ctx context.Context, deliveryID, event string) (claimed bool, err error) {
	if deliveryID == "" {
		// GitHub always sends X-GitHub-Delivery; missing means we cannot
		// dedupe — still process, but do not insert an empty PK.
		return true, nil
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO github_webhook_deliveries (delivery_id, event)
		VALUES ($1, $2)
	`, deliveryID, event)
	if err == nil {
		return true, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return false, nil
	}
	return false, err
}

// Seen reports whether delivery_id is already recorded (tests / diagnostics).
func (s *Store) Seen(ctx context.Context, deliveryID string) (bool, error) {
	var one int
	err := s.Pool.QueryRow(ctx, `
		SELECT 1 FROM github_webhook_deliveries WHERE delivery_id = $1
	`, deliveryID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
