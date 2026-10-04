-- +goose Up
-- Adds the two states a tunnel can be put into deliberately (Stop/Start
-- buttons on the Tunnels page), as opposed to 'failed' (something went
-- wrong) or 'deleting' (on its way out permanently). 'stopping'/'starting'
-- are the same kind of transient in-flight marker 'pending'/'deleting'
-- already are - the systemctl calls on both ends take a couple of seconds,
-- so the row visibly passes through them rather than jumping straight to
-- the end state.
ALTER TABLE tunnels DROP CONSTRAINT tunnels_status_check;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_status_check
    CHECK (status IN ('pending', 'active', 'failed', 'deleting', 'stopped', 'stopping', 'starting'));

-- +goose Down
ALTER TABLE tunnels DROP CONSTRAINT tunnels_status_check;
ALTER TABLE tunnels ADD CONSTRAINT tunnels_status_check
    CHECK (status IN ('pending', 'active', 'failed', 'deleting'));
