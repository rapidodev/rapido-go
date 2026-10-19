-- name: GetNodeSSHLogin :one
-- The SSH login a tunnel row holds for a node (the panel keeps one per tunnel,
-- not per node). An active tunnel is preferred, then the oldest.
SELECT node_ssh_port, node_ssh_user, node_ssh_password
FROM tunnels
WHERE node_id = $1 AND node_ssh_user <> ''
ORDER BY (status = 'active') DESC, id
LIMIT 1;

-- name: ListNodeIDsWithSSH :many
-- Nodes for which some tunnel row stores an SSH login.
SELECT DISTINCT node_id FROM tunnels WHERE node_ssh_user <> '';

-- name: ListTunnelIDsByNode :many
SELECT id FROM tunnels WHERE node_id = $1 ORDER BY id;
