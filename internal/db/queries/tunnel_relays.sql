-- name: ListTunnelRelays :many
SELECT * FROM tunnel_relays ORDER BY id;

-- name: CreateTunnelRelay :one
INSERT INTO tunnel_relays (name, host, port) VALUES ($1, $2, $3) RETURNING *;

-- name: DeleteTunnelRelay :exec
DELETE FROM tunnel_relays WHERE id = $1;

-- name: UpdateTunnelRelayTarget :exec
-- Keeps a tunnel's monitoring row aimed at the relay address and a public
-- port that still exists after the tunnel is edited.
UPDATE tunnel_relays SET name = $2, host = $3, port = $4 WHERE id = $1;
