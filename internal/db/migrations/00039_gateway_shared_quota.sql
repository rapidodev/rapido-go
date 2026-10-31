-- +goose Up
-- A panel's users, copied to another panel so one subscription can carry both panels'
-- servers, were pushed only when something changed them, so most never arrived and got
-- servers that refused them. And each panel counted traffic alone, so a user's limit
-- held per panel rather than in total.
--
-- gateway_peer_users is the panel that owns the users keeping track, per peer, of what
-- the peer holds: whether it accepted each one (the only users shown its servers), the
-- state last sent (anything else gets sent again, whichever route changed it), and the
-- peer's traffic counter as last read (so reading twice can't count anything twice).
CREATE TABLE gateway_peer_users (
    peer_id           INT NOT NULL REFERENCES gateway_peers (id) ON DELETE CASCADE,
    username          TEXT NOT NULL,
    state             TEXT NOT NULL CHECK (state IN ('synced', 'rejected')),
    state_hash        TEXT NOT NULL,
    detail            TEXT NOT NULL DEFAULT '',
    peer_used_traffic BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (peer_id, username)
);
CREATE INDEX gateway_peer_users_username ON gateway_peer_users (username);

-- What the admin's Gateway page shows of a peer without calling it.
ALTER TABLE gateway_peers
    ADD COLUMN last_seen_at TIMESTAMPTZ,
    ADD COLUMN last_error   TEXT NOT NULL DEFAULT '';

-- A panel's name can be blank or repeated, and a peer has to tell its origins apart to
-- report one's traffic to it and refuse another's writes.
ALTER TABLE gateway_settings
    ADD COLUMN instance_id TEXT NOT NULL DEFAULT md5(random()::text || clock_timestamp()::text);

ALTER TABLE users ADD COLUMN synced_origin_id TEXT;
CREATE INDEX users_synced_origin ON users (synced_origin_id, id) WHERE synced_origin_id IS NOT NULL;

-- +goose Down
DROP INDEX users_synced_origin;
ALTER TABLE users DROP COLUMN synced_origin_id;
ALTER TABLE gateway_settings DROP COLUMN instance_id;
ALTER TABLE gateway_peers DROP COLUMN last_seen_at, DROP COLUMN last_error;
DROP TABLE gateway_peer_users;
