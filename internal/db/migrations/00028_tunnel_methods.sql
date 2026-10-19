-- +goose Up
-- More than one way to build a tunnel. 'gre_frp' (GRE over IPv4 with frp
-- inside it) was the only design; the others are frp straight over the
-- relay's address (IPv6 or IPv4), frp over QUIC, and three kernel-forwarding
-- designs (GRE, GRE over IPv6, WireGuard) that drop the userspace proxy.
-- See internal/tunnelprovision/methods.go for what each one builds.
--
-- node_tunnel_addr / relay_tunnel_addr are the addresses the tunnel's own
-- data path uses when they differ from the SSH address - typically the
-- IPv6 pair for an IPv6 underlay while SSH stays on IPv4. NULL means "use
-- the SSH address" (nodes.address / relay_host), which is what every row
-- created before this migration does.
ALTER TABLE tunnels DROP CONSTRAINT IF EXISTS tunnels_method_check;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_method_check
    CHECK (method IN ('gre_frp', 'frp_direct', 'frp_quic', 'gre_dnat', 'ip6gre_dnat', 'wg_dnat'));
ALTER TABLE tunnels ADD COLUMN node_tunnel_addr TEXT;
ALTER TABLE tunnels ADD COLUMN relay_tunnel_addr TEXT;

-- +goose Down
ALTER TABLE tunnels DROP COLUMN relay_tunnel_addr;
ALTER TABLE tunnels DROP COLUMN node_tunnel_addr;
ALTER TABLE tunnels DROP CONSTRAINT IF EXISTS tunnels_method_check;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_method_check CHECK (method IN ('gre_frp')) NOT VALID;
