// Mirrors internal/httpapi/tunnelrelays.go's tunnelRelayDTO. A tunnel relay
// is an external TCP-forwarding box (a GRE+FRP relay or similar) that sits
// in front of a node but lives entirely outside this fleet - rapido-go has
// no other way to know it exists. There is no update/edit endpoint: only
// create and delete, matching the backend.
export type TunnelRelay = {
  id: number;
  name: string;
  host: string;
  port: number;

  // Live status, published by relayhealth's probe loop. All three are null
  // when nothing has been published yet - e.g. a relay added seconds ago
  // that hasn't had its first probe round.
  up: boolean | null;
  error: string | null;
  checked_at: string | null;
};

export type TunnelRelayWritePayload = {
  name: string;
  host: string;
  port: number;
};
