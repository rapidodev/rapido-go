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
    node_tunnel_addr, relay_tunnel_addr,
    managed, details,
    status
) VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14, $15,
    $17, $18,
    $19, $20,
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

-- name: UpdateTunnelEdit :one
-- The fields an admin can change on an existing tunnel. Everything that
-- decides how the tunnel is built (method, subnet, interface, frp token) is
-- deliberately not here - changing those means a new tunnel.
UPDATE tunnels SET
    name = $2,
    node_ssh_port = $3, node_ssh_user = $4, node_ssh_password = $5,
    relay_host = $6, relay_ssh_port = $7, relay_ssh_user = $8, relay_ssh_password = $9,
    ports = $10, details = $11
WHERE id = $1
RETURNING *;
