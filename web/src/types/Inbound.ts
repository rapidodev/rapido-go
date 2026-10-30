// GET /api/inbounds returns a raw map of protocol -> tag list
// (map[string][]string on the Go side - see internal/httpapi/inbounds.go's
// handleListInbounds) rather than the old Python-targeting frontend's
// objects-with-metadata shape (tag/protocol/network/tls/port). Per the plan's
// key fact #3, this makes inbound selection simpler than before: there is no
// per-inbound metadata to display, only tags to check off. Still used as-is
// by InboundsPicker.tsx (Users/User Templates forms) and by the reseller-API
// filtering logic server-side - the richer Inbound/InboundSyncEntry shapes
// below are what the merged Core Config page (rapido-ui/XrayConfigAdmin.tsx,
// via types/XrayConfig.ts) works with instead.
export type InboundsByProtocol = Record<string, string[]>;

// One entry of GET /api/inbounds (internal/httpapi/inbounds.go's
// proxyInboundDTO). `port` is the single value external API clients read;
// `ports` is every distinct enabled host port, ascending - an inbound that
// listens on many ports has them all here while `port` stays one of them.
export type InboundListEntry = {
  tag: string;
  protocol: string;
  network: string;
  tls: string;
  port: number;
  ports: number[];
};

export const INBOUND_NETWORKS = ["tcp", "ws", "grpc", "kcp", "quic", "splithttp", "xhttp"] as const;
export type InboundNetwork = (typeof INBOUND_NETWORKS)[number];
export const INBOUND_SECURITIES = ["none", "tls", "reality"] as const;
export type InboundSecurity = (typeof INBOUND_SECURITIES)[number];

// Mirrors internal/httpapi/inbounds.go: proxyTypeValid, the protocols
// syncInboundEntries refuses without TLS, validSnellV6Modes and
// shadowTLSInnerKeyLength.
export const INBOUND_PROTOCOLS = [
  "vless",
  "vmess",
  "trojan",
  "shadowsocks",
  "hysteria2",
  "tuic",
  "anytls",
  "hysteria",
  "naive",
  "snell",
  "shadowtls",
] as const;
export const TLS_REQUIRED_PROTOCOLS = ["hysteria2", "tuic", "anytls", "hysteria"] as const;
export const SNELL_V6_MODES = ["default", "unshaped", "unsafe-raw"] as const;
export const SHADOWTLS_INNER_KEY_BYTES: Record<string, number> = {
  "2022-blake3-aes-128-gcm": 16,
  "2022-blake3-aes-256-gcm": 32,
  "2022-blake3-chacha20-poly1305": 32,
};

// Mirrors internal/httpapi/inbounds.go's inboundDetailDTO - the full-fidelity
// shape the merged xray-config document carries its inbounds in, as opposed
// to the plain tag list above. reality_* fields are only meaningful (and
// only ever set) when security is "reality"; tls_* only when security is
// "tls" - a real customer-facing certificate/key pair, NOT the panel's own
// node-mTLS CA. An inbound with security "tls" and no tls_certificate/
// tls_key yet is valid but stays out of automatic node sync (see
// ListAutoSyncInbounds's own doc comment).
export type Inbound = {
  tag: string;
  protocol: string;
  network: string;
  header_type: string;
  security: string;
  reality_private_key?: string;
  reality_short_ids?: string[];
  reality_server_name?: string;
  reality_server_port?: number;
  tls_certificate?: string;
  tls_key?: string;
  tls_server_name?: string;
  hysteria2_obfs_password?: string;
  up_mbps?: number;
  down_mbps?: number;
  congestion_control?: string;
  zero_rtt_handshake?: boolean;
  snell_psk?: string;
  snell_v6_mode?: string;
  hysteria_obfs_password?: string;
  shadowtls_inner_method?: string;
  shadowtls_inner_password?: string;
};

// POST /api/inbounds/sync's per-entry shape (internal/httpapi/inbounds.go's
// inboundSyncEntry) - the same request body both creates a brand-new
// inbound and re-syncs an existing one's fields (an UPSERT by tag).
export type InboundSyncEntry = {
  tag: string;
  protocol: string;
  network?: string;
  header_type?: string;
  security?: string;
  reality_private_key?: string;
  reality_short_ids?: string[];
  reality_server_name?: string;
  reality_server_port?: number;
  tls_certificate?: string;
  tls_key?: string;
  tls_server_name?: string;
  hysteria2_obfs_password?: string;
  congestion_control?: string;
  zero_rtt_handshake?: boolean;
  snell_psk?: string;
  snell_v6_mode?: string;
  up_mbps?: number;
  down_mbps?: number;
  hysteria_obfs_password?: string;
  shadowtls_inner_method?: string;
  shadowtls_inner_password?: string;
};

// POST /api/inbounds's body - InboundSyncEntry plus the one extra field
// that endpoint alone understands: port, required, written straight onto
// the new inbound's host so it works immediately (see
// internal/httpapi/inbounds.go's handleCreateInbound). Leaving
// tls_certificate/tls_key/snell_psk out is deliberate and expected: the
// server auto-generates them for a protocol that needs one and wasn't
// given one, exactly what "+ Add inbound" relies on.
export type CreateInboundPayload = InboundSyncEntry & { port: number };
