// Mirrors internal/httpapi/gateway.go's gatewaySettingsDTO - this panel's
// own identity for the multi-panel load balancer. `secret` is returned in
// full (not masked, unlike every other secret in types/Backup.ts's
// siblings): its whole purpose is to be copy-pasted into a peer panel's
// "add peer" form.
export type GatewaySettings = {
  name: string;
  secret: string;
};

// Mirrors gatewayPeerDTO - a panel THIS install calls out to. `secret`
// here is the PEER's own inbound secret (what this panel sends as its
// Bearer token when calling that peer), not this panel's own.
export type GatewayPeer = {
  id: number;
  name: string;
  base_url: string;
  secret: string;
  enabled: boolean;
  // What the background job last learned about it, about every half a minute.
  last_seen_at: string | null;
  last_error: string;
  // Null until a pass has reached it, or once it has been out of reach for a while.
  crowdedness: number | null;
  hosts: GatewayPeerHost[] | null;
  // This panel's users it took and refused, out of total_users.
  synced_users: number;
  rejected_users: number;
  total_users: number;
};

export type GatewayPeerHost = {
  remark: string;
  protocol: string;
  network: string;
  security: string;
  address: string;
  port: number;
};

export type GatewayRejectedUser = {
  username: string;
  detail: string;
  at: string;
};

export type GatewayPeerDeleted = {
  deleted: boolean;
  copies_removed: number;
  // The peer couldn't be reached, so it still holds working copies of these users.
  copies_left: boolean;
};

export type GatewayPeerRequest = {
  name: string;
  base_url: string;
  secret: string;
  enabled?: boolean;
};

export type GatewayTestResult = {
  ok: boolean;
  panel_name?: string;
  detail?: string;
  // Both absent if the ping itself succeeded but the follow-up status call
  // (sub-phase 3) didn't - see internal/httpapi/gateway.go's
  // handleTestGatewayPeer, that failure never turns a real "ok" into "not ok".
  crowdedness?: number;
  host_count?: number;
};
