-- +goose Up
-- Tunnels the panel did not build. An admin often sets a tunnel up by hand
-- (frp, GRE, WireGuard, even HAProxy failover on top); the panel can still
-- find out what it is by reading both ends over SSH (internal/tunneldiscover)
-- and then only monitors it. managed = false marks such a row: the panel never
-- tears down, restarts or rewrites anything that it did not create.
--
-- details holds what discovery found (the protocol label, the NIC names to
-- measure, where the frp configs and the HAProxy admin socket live, the relay
-- addresses the node reaches the relay by) so monitoring can run without
-- re-discovering every round.
ALTER TABLE tunnels ADD COLUMN managed BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE tunnels ADD COLUMN details JSONB NOT NULL DEFAULT '{}'::jsonb;

-- 'external' = something is tunnelled but it is not one of the designs above.
ALTER TABLE tunnels DROP CONSTRAINT IF EXISTS tunnels_method_check;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_method_check
    CHECK (method IN ('gre_frp', 'frp_direct', 'frp_quic', 'gre_dnat', 'ip6gre_dnat', 'wg_dnat', 'external'));

-- +goose Down
ALTER TABLE tunnels DROP CONSTRAINT IF EXISTS tunnels_method_check;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_method_check
    CHECK (method IN ('gre_frp', 'frp_direct', 'frp_quic', 'gre_dnat', 'ip6gre_dnat', 'wg_dnat')) NOT VALID;
ALTER TABLE tunnels DROP COLUMN details;
ALTER TABLE tunnels DROP COLUMN managed;
