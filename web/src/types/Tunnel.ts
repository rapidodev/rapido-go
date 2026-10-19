// Mirrors internal/httpapi/tunnels.go's tunnelDTO. A Tunnel is both the
// provisioning record AND its own monitoring entry - the old standalone
// "Tunnel Relays" page (add a bare host:port for health-only probing) was
// folded in here, since every real relay this fleet has is a provisioned
// tunnel, and a tunnel already carries everything a health/resource check
// needs. `method` is the tunnel design (see TUNNEL_METHODS below and
// internal/tunnelprovision/methods.go for what each one builds).
export type TunnelStatus = "pending" | "active" | "failed" | "deleting" | "stopped" | "stopping" | "starting";

export const TUNNEL_METHODS = [
  "frp_direct",
  "ip6gre_dnat",
  "wg_dnat",
  "gre_dnat",
  "frp_quic",
  "gre_frp",
] as const;
export type TunnelMethod = (typeof TUNNEL_METHODS)[number];

export type TunnelHealth = {
  up: boolean;
  error?: string;
  checked_at?: string;
  since?: string;
  // Present only while down - how long ago `since` was, in seconds. The
  // same number an InfraAlert recovery message reports, shown live here
  // without waiting for a recovery.
  down_for_seconds?: number;
};

export type TunnelMetrics = {
  cpu_percent: number;
  mem_used_mb: number;
  mem_total_mb: number;
  disk_used_gb: number;
  disk_total_gb: number;
  rx_bytes: number;
  tx_bytes: number;
  // Cumulative loss counters straight from the GRE interface (since the
  // relay's last reboot) - real packet loss, not an estimate.
  rx_dropped: number;
  tx_dropped: number;
  rx_errors: number;
  tx_errors: number;
  // Live (not cumulative) count of established connections through this
  // tunnel right now.
  connections: number;
  checked_at: string;
};

export type Tunnel = {
  id: number;
  name: string;
  method: TunnelMethod;
  node_id: number;
  relay_host: string;
  ports: number[];
  interface_name: string;
  frp_control_port: number;
  // The addresses the tunnel itself runs on when they differ from the SSH
  // address (e.g. an IPv6 underlay); null = same as the SSH address.
  node_tunnel_addr: string | null;
  relay_tunnel_addr: string | null;
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
  method?: TunnelMethod;
  node_tunnel_addr?: string;
  relay_tunnel_addr?: string;
};

export type TunnelDeleteResult = {
  detail: string;
  teardown_warning: string | null;
};
