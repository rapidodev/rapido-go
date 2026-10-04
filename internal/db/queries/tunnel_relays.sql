-- name: ListTunnelRelays :many
SELECT * FROM tunnel_relays ORDER BY id;

-- name: CreateTunnelRelay :one
INSERT INTO tunnel_relays (name, host, port) VALUES ($1, $2, $3) RETURNING *;

-- name: DeleteTunnelRelay :exec
DELETE FROM tunnel_relays WHERE id = $1;
