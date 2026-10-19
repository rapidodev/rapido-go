-- +goose Up
-- Per-round measurements of every tunnel (internal/tunnelwatch writes one row
-- each time internal/tunnelmetrics finishes a collection round) and the
-- events derived from them. Both are history the dashboard charts and the
-- Network Doctor report read; neither is needed for the tunnels to work, so
-- they cascade away with the tunnel and are pruned by age.
--
-- Every measured column is nullable on purpose: a round in which the relay
-- (or the node) could not be reached stores NULL, not 0, so a chart shows a
-- gap instead of a false "no traffic".
CREATE TABLE tunnel_samples (
    tunnel_id     INTEGER          NOT NULL REFERENCES tunnels (id) ON DELETE CASCADE,
    at            TIMESTAMPTZ      NOT NULL,
    relay_rx_bps  DOUBLE PRECISION,
    relay_tx_bps  DOUBLE PRECISION,
    node_rx_bps   DOUBLE PRECISION,
    node_tx_bps   DOUBLE PRECISION,
    connections   INTEGER,
    cpu           REAL,
    -- Path quality from the node to the relay, over each address family:
    -- loss in percent, round-trip and jitter in milliseconds. NULL when the
    -- family was not measured (no address of that family) or, for the rtt
    -- columns, when every probe was lost.
    v6_rtt_ms     REAL,
    v6_loss       REAL,
    v6_jitter_ms  REAL,
    v4_rtt_ms     REAL,
    v4_loss       REAL,
    v4_jitter_ms  REAL,
    -- HAProxy failover in front of frpc: which server carries the sessions
    -- and whether the primary / the backup server is up. NULL when the node
    -- has no HAProxy to read.
    ha_active     TEXT,
    ha_primary_up BOOLEAN,
    ha_backup_up  BOOLEAN,
    PRIMARY KEY (tunnel_id, at)
);
CREATE INDEX tunnel_samples_at_idx ON tunnel_samples (at);

CREATE TABLE tunnel_events (
    id               BIGSERIAL   PRIMARY KEY,
    tunnel_id        INTEGER     NOT NULL REFERENCES tunnels (id) ON DELETE CASCADE,
    at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- tunnel_down, tunnel_up, failover, failback, loss_high, ceiling,
    -- nic_saturated
    kind             TEXT        NOT NULL,
    severity         TEXT        NOT NULL DEFAULT 'info'
        CHECK (severity IN ('info', 'warning', 'critical')),
    detail           TEXT        NOT NULL DEFAULT '',
    -- For tunnel_up: how long the tunnel was down.
    duration_seconds INTEGER
);
CREATE INDEX tunnel_events_tunnel_at_idx ON tunnel_events (tunnel_id, at DESC);
CREATE INDEX tunnel_events_at_idx ON tunnel_events (at DESC);

-- +goose Down
DROP TABLE tunnel_events;
DROP TABLE tunnel_samples;
