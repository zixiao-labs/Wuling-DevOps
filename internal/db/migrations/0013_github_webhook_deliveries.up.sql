-- Idempotency ledger for GitHub App webhook deliveries.
-- GitHub redelivers on timeout; without this key we would double-create runs.
CREATE TABLE github_webhook_deliveries (
    delivery_id  TEXT        PRIMARY KEY,
    event        TEXT        NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX github_webhook_deliveries_received_at_idx
    ON github_webhook_deliveries (received_at);
