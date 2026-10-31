-- name: GetGatewaySecret :one
SELECT * FROM gateway_settings ORDER BY id LIMIT 1;

-- name: CreateGatewaySecret :one
INSERT INTO gateway_settings (secret) VALUES ($1)
RETURNING *;

-- name: RotateGatewaySecret :one
UPDATE gateway_settings SET secret = $1
WHERE id = (SELECT id FROM gateway_settings ORDER BY id LIMIT 1)
RETURNING *;

-- name: SetGatewayName :one
UPDATE gateway_settings SET name = $1
WHERE id = (SELECT id FROM gateway_settings ORDER BY id LIMIT 1)
RETURNING *;

-- name: ListGatewayPeers :many
SELECT * FROM gateway_peers ORDER BY id;

-- name: GetGatewayPeer :one
SELECT * FROM gateway_peers WHERE id = $1;

-- name: CreateGatewayPeer :one
INSERT INTO gateway_peers (name, base_url, secret, enabled)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateGatewayPeer :one
UPDATE gateway_peers SET name = $2, base_url = $3, secret = $4, enabled = $5
WHERE id = $1
RETURNING *;

-- name: DeleteGatewayPeer :exec
DELETE FROM gateway_peers WHERE id = $1;

-- name: UpdateGatewayPeerHealth :exec
-- A pass over a peer that reached it moves last_seen_at. last_error is the pass's own
-- outcome, so it clears itself once the peer recovers.
UPDATE gateway_peers SET
    last_seen_at = CASE WHEN sqlc.arg(reached)::bool THEN now() ELSE last_seen_at END,
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id);

-- name: ListGatewayPendingUsers :many
-- This panel's own users whose state differs from what the peer last accepted, whichever
-- route changed it: an edit, a renewal, the review job, a bulk action. An answer of "no"
-- is asked again hourly, as what stood in the way may be gone by then. A disabled peer
-- keeps hearing about the users it holds, as their configs still work there, but gets
-- no new ones.
WITH state AS (
    SELECT u.username, u.status, u.data_limit, u.data_limit_reset_strategy, u.expire,
        COALESCE((SELECT jsonb_object_agg(p.type, p.settings) FROM proxies p WHERE p.user_id = u.id), '{}'::jsonb)::jsonb AS proxies
    FROM users u
    WHERE u.synced_from_panel_name IS NULL
), hashed AS (
    SELECT s.*, md5(s.status || '|' || COALESCE(s.data_limit::text, '') || '|' || s.data_limit_reset_strategy || '|'
        || COALESCE(s.expire::text, '') || '|' || s.proxies::text)::text AS state_hash
    FROM state s
)
SELECT h.username, h.status, h.data_limit, h.data_limit_reset_strategy, h.expire, h.proxies, h.state_hash
FROM hashed h
LEFT JOIN gateway_peer_users g ON g.peer_id = sqlc.arg(peer_id) AND g.username = h.username
WHERE (g.username IS NULL AND sqlc.arg(include_new)::bool)
   OR (g.username IS NOT NULL AND g.state_hash <> h.state_hash)
   OR (g.state = 'rejected' AND g.updated_at < now() - interval '1 hour')
ORDER BY h.username
LIMIT sqlc.arg(row_limit);

-- name: ListGatewayOrphanedUsers :many
-- What a peer holds of users this panel no longer has.
SELECT g.username, g.state FROM gateway_peer_users g
WHERE g.peer_id = $1 AND NOT EXISTS (
    SELECT 1 FROM users u WHERE u.username = g.username AND u.synced_from_panel_name IS NULL
)
ORDER BY g.username
LIMIT $2;

-- name: UpsertGatewayPeerUser :exec
-- peer_used_traffic is only ever set here on the first row, to what the peer had
-- counted by then: traffic from before this panel started adding it up was already
-- paid for, or it would limit people on the day a peer is switched on.
INSERT INTO gateway_peer_users (peer_id, username, state, state_hash, detail, peer_used_traffic)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (peer_id, username) DO UPDATE SET
    state = EXCLUDED.state, state_hash = EXCLUDED.state_hash, detail = EXCLUDED.detail, updated_at = now();

-- name: DeleteGatewayPeerUser :exec
DELETE FROM gateway_peer_users WHERE peer_id = $1 AND username = $2;

-- name: ClearGatewayRejectedUsers :execrows
-- Forgets every refusal, so they are all asked again on the next pass.
DELETE FROM gateway_peer_users WHERE peer_id = $1 AND state = 'rejected';

-- name: ListGatewayRejectedUsers :many
SELECT username, detail, updated_at FROM gateway_peer_users
WHERE peer_id = $1 AND state = 'rejected'
ORDER BY username
LIMIT $2;

-- name: CountGatewayPeerUsers :many
SELECT peer_id, state, count(*) AS users FROM gateway_peer_users GROUP BY peer_id, state;

-- name: CountLocalUsers :one
SELECT count(*) FROM users WHERE synced_from_panel_name IS NULL;

-- name: ListGatewayPeersHoldingUser :many
-- The peers that accepted this user, the only ones whose servers will let them in.
SELECT peer_id FROM gateway_peer_users WHERE username = $1 AND state = 'synced';

-- name: ClaimGatewayPeerUsage :many
-- Turns the peer's running counters into traffic not yet counted here, and records them
-- as counted, in one statement: inside the transaction that adds that traffic up, so a
-- pass that fails counts nothing and the next one reads the same counters again. A
-- counter that went down belongs to a copy made anew, and all of it is new.
WITH seen AS (
    SELECT unnest(sqlc.arg(usernames)::text[]) AS username, unnest(sqlc.arg(counters)::bigint[]) AS counter
), old AS (
    SELECT g.username, g.peer_used_traffic, s.counter
    FROM gateway_peer_users g JOIN seen s USING (username)
    WHERE g.peer_id = sqlc.arg(peer_id) AND g.state = 'synced' AND g.peer_used_traffic <> s.counter
    FOR UPDATE OF g
), moved AS (
    UPDATE gateway_peer_users g SET peer_used_traffic = old.counter
    FROM old WHERE g.peer_id = sqlc.arg(peer_id) AND g.username = old.username
)
SELECT old.username, (CASE WHEN old.counter > old.peer_used_traffic THEN old.counter - old.peer_used_traffic ELSE old.counter END)::bigint AS delta
FROM old;

-- name: ListGatewayReplicaUsage :many
-- Peer side: one origin's copies that have moved any traffic, a page at a time.
SELECT id, username, used_traffic FROM users
WHERE synced_origin_id = sqlc.arg(origin_id) AND id > sqlc.arg(after_id) AND used_traffic > 0
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: PurgeGatewayReplicas :execrows
-- Peer side: an origin has let go of this panel, so nothing keeps its copies current.
DELETE FROM users WHERE synced_origin_id = $1;
