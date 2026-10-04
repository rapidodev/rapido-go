-- +goose Up
-- External TCP-forwarding boxes (GRE+FRP relays etc.) that sit in front of
-- a node but live entirely outside this fleet - rapido-go has no other way
-- to know they exist. Each row is one endpoint worth probing: reaching it
-- exercises the whole path (relay up, its tunnel to the node up, frps
-- running, the node's own inbound listening), so a single TCP dial is a
-- meaningful proxy for "is this relay actually forwarding traffic" without
-- needing SSH access or a provider-specific bandwidth-quota API.
CREATE TABLE tunnel_relays (
    id         SERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    host       TEXT NOT NULL,
    port       INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE tunnel_relays;
