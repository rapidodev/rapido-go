-- +goose Up
-- A panel-provisioned tunnel: unlike tunnel_relays (pure monitoring of a
-- relay someone already set up by hand), a row here is something this
-- panel itself built - it SSH'd into the relay box and pushed config to
-- the node agent to bring both ends of a GRE+FRP (for now; method exists
-- so a second kind, e.g. wireguard_frp, can be added later without a
-- schema change) tunnel up, and can tear both back down again from the
-- Delete button. credential storage matches every other secret this panel
-- already holds (reseller_api_secret, telegram_api_token, node TLS
-- keys, ...): plain columns, no separate encryption-at-rest layer - DB
-- access control is the existing boundary for all of them, and this is
-- one more thing behind it, not a special case.
CREATE TABLE tunnels (
    id                SERIAL PRIMARY KEY,
    name              TEXT NOT NULL,
    method            TEXT NOT NULL DEFAULT 'gre_frp' CHECK (method IN ('gre_frp')),
    node_id           INTEGER NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    -- The node side is provisioned over SSH too, not through the node
    -- agent's own HTTPS API - that API has no "manage a GRE interface and
    -- run frpc" capability today, and adding one would mean releasing and
    -- rolling out a new node-agent version before this feature could work
    -- at all. SSH is the one mechanism this panel can use against both
    -- ends right now; host/port are read from the nodes row itself
    -- (nodes.address), only the login needs to be supplied here.
    node_ssh_port     INTEGER NOT NULL DEFAULT 22,
    node_ssh_user     TEXT NOT NULL,
    node_ssh_password TEXT NOT NULL,
    -- The relay box's own identity - an external server outside this
    -- fleet, reached over SSH only for the duration of provisioning/
    -- teardown, never held open.
    relay_host        TEXT NOT NULL,
    relay_ssh_port    INTEGER NOT NULL DEFAULT 22,
    relay_ssh_user    TEXT NOT NULL,
    relay_ssh_password TEXT NOT NULL,
    -- Ports this tunnel forwards into the node, e.g. one or more of its
    -- VLESS location ports.
    ports             INTEGER[] NOT NULL,
    -- Per-tunnel allocated parameters a provisioning run computes once and
    -- stores, so re-reading a tunnel's state (status page, teardown) never
    -- has to re-derive them and risk landing on a different value the
    -- second time:
    --   interface_name    the GRE interface name on both ends (<=15
    --                      chars, no spaces - see cmd/node/cli.go's own
    --                      interface-naming constraint)
    --   tunnel_subnet      the /30 both ends' tunnel-internal addresses
    --                      come from (node = .1, relay = .2, matching
    --                      every tunnel already running on this fleet)
    --   frp_control_port   the frps control port on the relay
    --   frp_token          the shared auth token frpc/frps both use
    interface_name    TEXT NOT NULL,
    tunnel_subnet     CIDR NOT NULL,
    frp_control_port  INTEGER NOT NULL,
    frp_token         TEXT NOT NULL,
    -- Ties this tunnel to its monitoring row (internal/relayhealth) once
    -- provisioning succeeds - set null, not cascaded, if that row is ever
    -- deleted independently on the Tunnel Relays page, so a tunnel this
    -- feature built doesn't silently vanish because its monitoring entry
    -- did.
    tunnel_relay_id   INTEGER REFERENCES tunnel_relays (id) ON DELETE SET NULL,
    status            TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active', 'failed', 'deleting')),
    status_message    TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tunnels_interface_name_key ON tunnels (interface_name);

-- +goose Down
DROP TABLE tunnels;
