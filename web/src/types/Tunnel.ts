// Mirrors internal/httpapi/tunnels.go's tunnelDTO. A Tunnel is both the
// provisioning record AND its own monitoring entry - the old standalone
// "Tunnel Relays" page (add a bare host:port for health-only probing) was
// folded in here, since every real relay this fleet has is a provisioned
// tunnel, and a tunnel already carries everything a health/resource check
// needs. `method` only has one value today ("gre_frp") - the field exists
// so a second kind (e.g. wireguard_frp) can be added later without a
// schema or DTO change.
export type TunnelStatus = "pending" | "active" | "failed" | "deleting" | "stopped" | "stopping" | "starting";

export type TunnelHealth = {
  up: boolean;
  error?: string;
  checked_at?: string;
};

export type TunnelMetrics = {
  cpu_percent: number;
  mem_used_mb: number;
  mem_total_mb: number;
  disk_used_gb: number;
  disk_total_gb: number;
  rx_bytes: number;
  tx_bytes: number;
  checked_at: string;
};

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

  // Live data, published by the backend singleton (internal/relayhealth +
  // internal/tunnelmetrics) - both null until its first probe/collection
  // round, e.g. for a tunnel created seconds ago.
  health: TunnelHealth | null;
  metrics: TunnelMetrics | null;
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
