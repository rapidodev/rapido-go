// Mirrors internal/httpapi/tunnels.go's tunnelDTO. Unlike types/TunnelRelay.ts
// (pure monitoring of a relay someone already set up by hand), a Tunnel here
// is something the panel itself provisions end to end: it SSHes into both the
// relay and the node, brings up a GRE interface on each side, and runs
// frps/frpc to forward `ports` from the relay into the node. `method` only
// has one value today ("gre_frp") - the field exists so a second kind (e.g.
// wireguard_frp) can be added later without a schema or DTO change.
export type TunnelStatus = "pending" | "active" | "failed" | "deleting";

export type Tunnel = {
  id: number;
  name: string;
  method: "gre_frp";
  node_id: number;
  relay_host: string;
  ports: number[];
  interface_name: string;
  frp_control_port: number;
  status: TunnelStatus;
  status_message: string | null;
  tunnel_relay_id: number | null;
};

// POST /api/tunnels body. Credentials are sent once, used immediately by the
// backend to provision both ends, and stored plainly server-side (matching
// every other secret this panel already holds) - there is no edit endpoint,
// only create and delete.
export type TunnelCreatePayload = {
  name: string;
  node_id: number;
  node_ssh_port?: number;
  node_ssh_user: string;
  node_ssh_password: string;
  relay_host: string;
  relay_ssh_port?: number;
  relay_ssh_user: string;
  relay_ssh_password: string;
  ports: number[];
};

export type TunnelDeleteResult = {
  detail: string;
  teardown_warning: string | null;
};
