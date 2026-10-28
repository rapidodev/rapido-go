-- +goose Up
CREATE TABLE node_report_receipts (
 node_id integer NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 report_id uuid NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(node_id,report_id)
);
CREATE INDEX node_report_receipts_at_idx ON node_report_receipts(received_at);
CREATE TABLE core_config_snapshots (
 id bigserial PRIMARY KEY,
 created_at timestamptz NOT NULL DEFAULT now(),
 config jsonb NOT NULL,
 reason text NOT NULL
);
CREATE TABLE delivery_outbox (
 id bigserial PRIMARY KEY,
 kind text NOT NULL,
 destination text NOT NULL,
 payload jsonb NOT NULL,
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX delivery_outbox_ready_idx ON delivery_outbox(available_at,id);
ALTER TABLE users ADD COLUMN last_usage_reset_at timestamptz;
-- Only the canonical signing identity may exist. Protect concurrent first boot.
DELETE FROM jwt_secrets WHERE id NOT IN (SELECT min(id) FROM jwt_secrets);
UPDATE jwt_secrets SET id=1 WHERE id!=1;
ALTER TABLE jwt_secrets ADD CONSTRAINT jwt_secrets_singleton CHECK(id=1);
CREATE INDEX users_admin_created_idx ON users(admin_id,created_at DESC,id DESC);
CREATE INDEX host_metrics_node_latest_idx ON host_metrics(node_id,id DESC);
CREATE INDEX node_user_usages_user_time_idx ON node_user_usages(user_id,created_at);
CREATE TRIGGER node_config_version_exclusions AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON exclude_inbounds_association FOR EACH STATEMENT EXECUTE FUNCTION bump_node_config_version();
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX users_created_page_idx ON users(created_at DESC,id DESC);
CREATE INDEX users_username_search_idx ON users USING gin(username gin_trgm_ops);
CREATE INDEX users_note_search_idx ON users USING gin(note gin_trgm_ops);
CREATE INDEX proxies_uuid_search_idx ON proxies USING gin((replace(lower(settings->>'id'),'-','')) gin_trgm_ops);
CREATE INDEX proxies_password_search_idx ON proxies USING gin((settings->>'password') gin_trgm_ops);
-- +goose Down
DROP INDEX proxies_password_search_idx;
DROP INDEX proxies_uuid_search_idx;
DROP INDEX users_note_search_idx;
DROP INDEX users_username_search_idx;
DROP INDEX users_created_page_idx;
DROP TRIGGER node_config_version_exclusions ON exclude_inbounds_association;
DROP INDEX node_user_usages_user_time_idx;
DROP INDEX host_metrics_node_latest_idx;
DROP INDEX users_admin_created_idx;
ALTER TABLE jwt_secrets DROP CONSTRAINT jwt_secrets_singleton;
ALTER TABLE users DROP COLUMN last_usage_reset_at;
DROP TABLE delivery_outbox;
DROP TABLE core_config_snapshots;
DROP TABLE node_report_receipts;
