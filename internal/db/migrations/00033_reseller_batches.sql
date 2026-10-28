-- +goose Up
CREATE TABLE reseller_delivery_batches(
 id text PRIMARY KEY,
 destination text NOT NULL,
 secret text NOT NULL,
 payload jsonb NOT NULL,
 admin_ids integer[] NOT NULL,
 deltas bigint[] NOT NULL,
 active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX reseller_one_active_batch ON reseller_delivery_batches(active) WHERE active;
-- +goose Down
DROP TABLE reseller_delivery_batches;
