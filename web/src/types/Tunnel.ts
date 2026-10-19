// Mirrors internal/httpapi/tunnels.go's tunnelDTO. A Tunnel is both the
// provisioning record AND its own monitoring entry - the old standalone
// "Tunnel Relays" page (add a bare host:port for health-only probing) was
// folded in here, since every real relay this fleet has is a provisioned
// tunnel, and a tunnel already carries everything a health/resource check
// needs. `method` is the tunnel design (see TUNNEL_METHODS below and
// internal/tunnelprovision/methods.go for what each one builds).
export type TunnelStatus = "pending" | "active" | "failed" | "deleting" | "stopped" | "stopping" | "starting";

// The designs the panel can build itself - what the create form offers.
export const TUNNEL_METHODS = [
  "frp_direct",
  "ip6gre_dnat",
  "wg_dnat",
  "gre_dnat",
  "frp_quic",
  "gre_frp",
] as const;
export type CreatableTunnelMethod = (typeof TUNNEL_METHODS)[number];
// Everything a tunnel row can be: the creatable designs plus "external", a
// tunnel that was found already running (detected) rather than built here.
export type TunnelMethod = CreatableTunnelMethod | "external";

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

  // The RELAY's main network card, in bits per second (rates) and bytes
  // (cumulative). All optional-safe: a backend older than this field, or a
  // relay whose card could not be read, leaves them out - treat as unknown.
  nic?: string;
  nic_speed_mbps?: number;
  nic_rx_bps?: number;
  nic_tx_bps?: number;
  nic_rx_bytes?: number;
  nic_tx_bytes?: number;
  // The node end of the tunnel, when the panel could read it.
  node?: TunnelNodeMetrics;
};

export type HaproxyServer = {
  backend: string;
  name: string;
  addr?: string;
  // HAProxy's own wording: "UP", "DOWN", "UP 1/2", "MAINT", ...
  status: string;
  backup: boolean;
  sessions: number;
  total_sessions: number;
  // How long the current status has held.
  since_seconds: number;
};

export type HaproxyInfo = {
  present: boolean;
  // Which server is carrying traffic right now.
  active?: string;
  servers?: HaproxyServer[];
};

export type TunnelNodeMetrics = {
  nic?: string;
  nic_speed_mbps?: number;
  rx_bps: number;
  tx_bps: number;
  rx_bytes: number;
  tx_bytes: number;
  error?: string;
  haproxy?: HaproxyInfo;
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

  // false = found already running by detection: the panel only monitors it
  // and never starts, stops, restarts or reconfigures anything on the servers.
  managed: boolean;
  // Human description, e.g. "frp over TCP via HAProxy (IPv6 with IPv4 backup)".
  protocol: string | null;
  // Every address the node reaches the relay by, primary first.
  relay_addrs: string[];
  // SSH login details. Passwords are never returned.
  node_ssh_port: number;
  node_ssh_user: string;
  relay_ssh_port: number;
  relay_ssh_user: string;
  node_nic: string | null;
  relay_nic: string | null;

  // Live data, published by the backend singleton (internal/relayhealth +
  // internal/tunnelmetrics) - both null until its first probe/collection
  // round, e.g. for a tunnel created seconds ago.
  health: TunnelHealth | null;
  metrics: TunnelMetrics | null;
};

// POST /api/tunnels body. Credentials are sent once, used immediately by the
// backend to provision both ends, and stored plainly server-side (matching
// every other secret this panel already holds). Editing them later goes
// through TunnelUpdatePayload.
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
  method?: CreatableTunnelMethod;
  node_tunnel_addr?: string;
  relay_tunnel_addr?: string;
};

// POST /api/tunnels/discover - look at a node (and, with its login, the
// relay) and report an already-running tunnel without changing anything.
export type TunnelDiscoverPayload = {
  node_id: number;
  node_ssh_port?: number;
  node_ssh_user: string;
  node_ssh_password: string;
  relay_host?: string;
  relay_ssh_port?: number;
  relay_ssh_user?: string;
  relay_ssh_password?: string;
};

export type TunnelDiscoverResult = {
  found: boolean;
  method: string;
  protocol: string;
  relay_host: string | null;
  relay_addrs: string[];
  control_port: number;
  ports: number[];
  interface: string;
  haproxy: boolean;
  frp_transport: string | null;
  node_nic: string | null;
  relay_nic: string | null;
  // false = the relay could not be read (no login given, or it failed): ask
  // for the relay login and discover again.
  relay_read: boolean;
  notes: string[];
};

// POST /api/tunnels/adopt - record a detected tunnel so the panel monitors it.
export type TunnelAdoptPayload = {
  name: string;
  node_id: number;
  node_ssh_port?: number;
  node_ssh_user: string;
  node_ssh_password: string;
  relay_host: string;
  relay_ssh_port?: number;
  relay_ssh_user: string;
  relay_ssh_password: string;
};

// PATCH /api/tunnels/:id - every field optional. An omitted or empty password
// keeps the stored one. For a managed tunnel a changed `ports` reconfigures
// the live tunnel (status goes to "starting" and back to "active"); for a
// detected one the ports are only what is monitored.
export type TunnelUpdatePayload = {
  name?: string;
  node_ssh_port?: number;
  node_ssh_user?: string;
  node_ssh_password?: string;
  relay_host?: string;
  relay_ssh_port?: number;
  relay_ssh_user?: string;
  relay_ssh_password?: string;
  ports?: number[];
};

export type TunnelDeleteResult = {
  detail: string;
  teardown_warning: string | null;
};

// GET /api/tunnels/:id/samples?hours= (sudo) - one point per bucket, averaged;
// every measured field is null when nothing was measured in the bucket. See
// docs/api-network-features.md section 1. Only the fields the dashboard draws
// are listed.
export type TunnelSamplePoint = {
  t: string;
  relay_rx_bps: number | null;
  relay_tx_bps: number | null;
  node_rx_bps: number | null;
  node_tx_bps: number | null;
  connections: number | null;
  cpu: number | null;
};

export type TunnelSamples = {
  bucket_seconds: number;
  hours: number;
  points: TunnelSamplePoint[];
};
