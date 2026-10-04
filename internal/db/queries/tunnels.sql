-- name: ListTunnels :many
SELECT * FROM tunnels ORDER BY id;

-- name: GetTunnel :one
SELECT * FROM tunnels WHERE id = $1;

-- name: CreateTunnel :one
INSERT INTO tunnels (
    name, method, node_id,
    node_ssh_port, node_ssh_user, node_ssh_password,
    relay_host, relay_ssh_port, relay_ssh_user, relay_ssh_password,
    ports, interface_name, tunnel_subnet, frp_control_port, frp_token,
    status
) VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14, $15,
    $16
) RETURNING *;

-- name: UpdateTunnelStatus :exec
UPDATE tunnels SET status = $2, status_message = $3 WHERE id = $1;

-- name: SetTunnelRelayID :exec
UPDATE tunnels SET tunnel_relay_id = $2 WHERE id = $1;

-- name: DeleteTunnel :exec
DELETE FROM tunnels WHERE id = $1;

-- name: ListUsedTunnelSubnets :many
-- Every /30 already allocated to a tunnel - the allocator in
-- internal/tunnelprovision reads this to pick the next free one rather
-- than ever handing out two tunnels the same subnet.
SELECT tunnel_subnet FROM tunnels;

-- name: ListUsedFRPControlPorts :many
SELECT frp_control_port FROM tunnels WHERE relay_host = $1;
