// Package notification defines the durable producer boundary for Wuling
// notifications. Delivery workers and user-facing notification APIs can be
// added later without coupling domain code to a particular channel.
package notification

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/db"
)

// Event is one domain event waiting to be delivered by the future Wuling
// notification system. DedupeKey must identify the logical event across
// webhook retries.
type Event struct {
	DedupeKey string
	Type      string
	OrgID     uuid.UUID
	RepoID    uuid.UUID
	Payload   any
}

// Publisher is the notification integration boundary used by domain code.
type Publisher interface {
	Publish(context.Context, Event) error
}

// Outbox persists events until a notification dispatcher is implemented.
type Outbox struct {
	Pool *db.Pool
}

// Publish appends an event idempotently. A repeated webhook delivery with the
// same DedupeKey is acknowledged without creating a second notification.
func (o *Outbox) Publish(ctx context.Context, event Event) error {
	if o == nil || o.Pool == nil {
		return nil
	}
	if event.DedupeKey == "" || event.Type == "" {
		return fmt.Errorf("notification event requires dedupe key and type")
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal notification payload: %w", err)
	}
	_, err = o.Pool.Exec(ctx, `
		INSERT INTO notification_outbox
			(id, dedupe_key, event_type, org_id, repo_id, payload)
		VALUES ($1, $2, $3, NULLIF($4::text, '')::uuid, NULLIF($5::text, '')::uuid, $6::jsonb)
		ON CONFLICT (dedupe_key) DO NOTHING
	`, uuid.New(), event.DedupeKey, event.Type, nullableUUID(event.OrgID), nullableUUID(event.RepoID), string(payload))
	if err != nil {
		return fmt.Errorf("publish notification event: %w", err)
	}
	return nil
}

func nullableUUID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
