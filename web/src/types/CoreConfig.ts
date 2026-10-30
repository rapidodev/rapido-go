// Mirrors internal/httpapi/coreconfig.go's DTOs exactly (field names, JSON
// keys, and the fixed enums the server validates against in
// validateCoreConfig). This is a genuinely new, structured shape - not a
// port of the old (Xray) dashboard's raw-JSON CoreSettings.tsx, which has no
// equivalent here because the underlying engine is sing-box, not Xray, and
// the two config shapes are not interchangeable.

export const LOG_LEVELS = [
  "trace",
  "debug",
  "info",
  "warn",
  "error",
  "fatal",
  "panic",
] as const;
export type LogLevel = (typeof LOG_LEVELS)[number];

export const OUTBOUND_TYPES = [
  "direct",
  "block",
  "socks",
  "http",
  "shadowsocks",
  "vmess",
  "trojan",
  "vless",
  "hysteria2",
  "tuic",
  "anytls",
  "hysteria",
  "shadowtls",
  "snell",
  "ssh",
  "tor",
  "selector",
  "urltest",
] as const;
export type OutboundType = (typeof OUTBOUND_TYPES)[number];

// Every outbound type validateCoreConfig accepts - not sing-box's full
// catalog. WireGuard is deliberately excluded: sing-box models it as an
// "Endpoint", a structurally different top-level config section this rewrite
// doesn't support yet, not just another outbound type (see coreconfig.go's
// own doc comment).

/** Group types: they pick among other outbounds and dial nothing themselves.
 * The Core Config page calls these balancers. */
export const OUTBOUND_GROUP_TYPES = ["selector", "urltest"] as const;

export const SHADOWSOCKS_METHODS = [
  "none",
  "aes-128-gcm",
  "aes-192-gcm",
  "aes-256-gcm",
  "chacha20-ietf-poly1305",
  "xchacha20-ietf-poly1305",
  "2022-blake3-aes-128-gcm",
  "2022-blake3-aes-256-gcm",
  "2022-blake3-chacha20-poly1305",
] as const;
export type ShadowsocksMethod = (typeof SHADOWSOCKS_METHODS)[number];

export const VMESS_SECURITY_TYPES = ["auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305"] as const;
export type VmessSecurity = (typeof VMESS_SECURITY_TYPES)[number];

export const CONGESTION_CONTROL_TYPES = ["cubic", "new_reno", "bbr"] as const;
export type CongestionControl = (typeof CONGESTION_CONTROL_TYPES)[number];

export const SNELL_OBFS_MODES = ["none", "http", "tls"] as const;
export const SHADOWTLS_VERSIONS = [1, 2, 3] as const;
export const VLESS_FLOWS = ["xtls-rprx-vision"] as const;

// The two tags a node always has even with zero custom outbounds configured
// (cmd/node/main.go's buildOptions always emits both) - reserved, so an
// admin can never define a custom outbound under either name (see
// validateCoreConfig's own check in coreconfig.go).
export const IMPLICIT_OUTBOUND_TAGS = ["direct", "block"] as const;
export type ImplicitOutboundTag = (typeof IMPLICIT_OUTBOUND_TAGS)[number];

export type Outbound = {
  tag: string;
  type: OutboundType;
  server?: string;
  server_port?: number;
  username?: string;
  password?: string;
  /** selector/urltest member tags only - ignored for every other type. */
  outbounds?: string[];

  /** vmess / vless / tuic */
  uuid?: string;
  /** vless (optional - e.g. "xtls-rprx-vision") */
  flow?: string;
  /** shadowsocks */
  method?: ShadowsocksMethod;
  /** vmess encryption */
  security?: VmessSecurity;
  /** tuic */
  congestion_control?: CongestionControl;
  /** shadowtls */
  version?: number;
  /** snell */
  psk?: string;
  obfs_mode?: string;
  obfs_host?: string;
  /** hysteria (v1) */
  obfs?: string;
  up_mbps?: number;
  down_mbps?: number;
  /** tor */
  executable_path?: string;
  data_directory?: string;
  /** urltest: the address its members are measured against, how often, and the
   * lead in milliseconds a member needs before the group switches to it. */
  url?: string;
  interval?: string;
  tolerance?: number;
  /** selector: the member it uses. The first one when unset. */
  default?: string;

  /** Shared TLS subset for vmess/trojan/vless (genuinely optional there)
   * and hysteria2/tuic (mandatory at the transport level for those two -
   * the form hides this toggle and always sends tls_enabled: true for
   * them, since QUIC requires TLS). */
  tls_enabled?: boolean;
  tls_server_name?: string;
  tls_insecure?: boolean;

  /** Binds this outbound's dialer to a specific network interface (e.g. a
   * WireGuard tunnel device name) - the sing-box equivalent of Xray's
   * streamSettings.sockopt.interface, which real per-location exit
   * selection depends on. Meaningless for selector/urltest. */
  bind_interface?: string;
  /** When bind_interface's device is down, dial directly instead of failing
   * (users keep working but exit from the server's own IP). Server-side
   * only allowed with a non-empty bind_interface on a leaf type - not
   * selector/urltest/block. Send it only when true. */
  direct_fallback?: boolean;
};

export const NETWORK_TYPES = ["tcp", "udp", "icmp"] as const;
export type NetworkType = (typeof NETWORK_TYPES)[number];

export const PROTOCOL_TYPES = [
  "tls",
  "http",
  "quic",
  "dns",
  "stun",
  "bittorrent",
  "dtls",
  "ssh",
  "rdp",
  "ntp",
] as const;
export type ProtocolType = (typeof PROTOCOL_TYPES)[number];

export type RoutingRule = {
  /** Matches by which inbound tag a connection arrived through - entries
   * must be real inbound tags (validated server-side against the live
   * inbounds table, not against this config's own data like outbound_tag
   * is). This is what lets one rule route e.g. only "node1"'s traffic to
   * a specific outbound. */
  inbound?: string[];
  /** Only match connections that arrived on these local listen ports of the
   * selected inbound(s). Server-side requires a non-empty `inbound`, every
   * port in 1..65535 and no duplicates. Omit when empty. */
  inbound_port?: number[];
  domain?: string[];
  domain_suffix?: string[];
  domain_keyword?: string[];
  ip_cidr?: string[];
  ip_is_private?: boolean;
  port?: number[];
  /** Each entry is a literal "start:end" string, e.g. "1000:2000". */
  port_range?: string[];
  network?: NetworkType[];
  protocol?: ProtocolType[];
  /** The matchers an older node agent doesn't know: the panel only sends a rule
   * using one of these to agents that say they understand them. */
  domain_regex?: string[];
  source_ip_cidr?: string[];
  source_ip_is_private?: boolean;
  source_port?: number[];
  source_port_range?: string[];
  auth_user?: string[];
  /** Must resolve to "direct", "block", or a tag in this config's own
   * outbounds - enforced server-side by validateCoreConfig. */
  outbound_tag: string;
};

export const DNS_SERVER_TYPES = ["local", "udp", "tcp", "tls", "https"] as const;
export type DNSServerType = (typeof DNS_SERVER_TYPES)[number];

export type DNSServer = {
  tag: string;
  type: DNSServerType;
  /** Required for every type except "local". */
  address?: string;
  port?: number;
  /** "https" only. */
  path?: string;
};

export type CoreConfig = {
  log_level: string;
  sniff_enabled: boolean;
  outbounds: Outbound[];
  routing_rules: RoutingRule[];
  dns_servers: DNSServer[];
  updated_at?: string | null;
};

// A node's own layer over the fleet CoreConfig above (nodes.core_overrides,
// see coreOverridesDTO in internal/httpapi/nodeprofile.go). Every key is
// optional and only that node is affected; the panel checks that the merged
// result is a valid config before saving it.
export const NODE_CORE_OVERRIDE_KEYS = [
  "log_level",
  "sniff_enabled",
  "dns_servers",
  "outbounds",
  "routing_rules_first",
] as const;
export type NodeCoreOverrideKey = (typeof NODE_CORE_OVERRIDE_KEYS)[number];

export type NodeCoreOverrides = {
  log_level?: LogLevel;
  sniff_enabled?: boolean;
  /** Replaces the fleet list outright; [] means "no DNS servers on this node". */
  dns_servers?: DNSServer[];
  /** Merged by tag: the same tag replaces the fleet outbound, a new tag is
   * appended. A tag can never remove one. */
  outbounds?: Outbound[];
  /** Placed before the fleet's routing rules, so they win for what they match. */
  routing_rules_first?: RoutingRule[];
};
