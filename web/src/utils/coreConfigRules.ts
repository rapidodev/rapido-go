import {
  CONGESTION_CONTROL_TYPES,
  DNS_SERVER_TYPES,
  IMPLICIT_OUTBOUND_TAGS,
  LOG_LEVELS,
  NETWORK_TYPES,
  OUTBOUND_GROUP_TYPES,
  OUTBOUND_TYPES,
  PROTOCOL_TYPES,
  RoutingRule,
  SHADOWSOCKS_METHODS,
  SNELL_OBFS_MODES,
  VLESS_FLOWS,
  VMESS_SECURITY_TYPES,
} from "types/CoreConfig";
import { INBOUND_PROTOCOLS, SHADOWTLS_INNER_KEY_BYTES, SNELL_V6_MODES, TLS_REQUIRED_PROTOCOLS } from "types/Inbound";
import { MAX_PORT, MIN_PORT, validateInboundPortField } from "utils/inboundPorts";

// The Core Config page edits one JSON document, through forms and as code, so
// what follows is pure helpers over plain objects: the forms rewrite the
// document through them, and Apply is gated on validateCoreConfigDoc so a
// payload the server would reject never leaves the browser. The checks mirror
// validateCoreConfig (coreconfig.go) and syncInboundEntries (inbounds.go).

export type Item = Record<string, unknown>;

export const parseDocObject = (text: string): Item | null => {
  try {
    const parsed = JSON.parse(text);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? parsed : null;
  } catch {
    return null;
  }
};

export const isObject = (v: unknown): v is Item => typeof v === "object" && v !== null && !Array.isArray(v);
export const asList = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
export const asStrings = (v: unknown): string[] => asList(v).filter((x): x is string => typeof x === "string");
export const str = (v: unknown): string => (typeof v === "string" ? v : "");

const within = (list: readonly (string | number)[], v: unknown) => list.includes(v as string);
const isWhole = (v: unknown): v is number => typeof v === "number" && Number.isInteger(v);
const isPortNumber = (v: unknown) => isWhole(v) && v >= MIN_PORT && v <= MAX_PORT;

const GROUP_OR_BLOCK_TYPES = new Set<string>([...OUTBOUND_GROUP_TYPES, "block"]);

/** The outbound types that need somewhere to dial: everything but direct, block, tor and the groups. */
export const NEEDS_SERVER_TYPES = new Set([
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
]);

export const isGroupOutbound = (o: unknown): boolean => isObject(o) && within(OUTBOUND_GROUP_TYPES, o.type);

/** A "leaf" outbound actually dials somewhere; selector/urltest pick among
 * other outbounds and block dials nothing. */
export const isLeafOutboundType = (type: unknown): boolean =>
  typeof type === "string" && type !== "" && !GROUP_OR_BLOCK_TYPES.has(type);

const hasBindInterface = (value: unknown): value is string =>
  typeof value === "string" && value.trim() !== "";

/** direct_fallback only makes sense for a leaf outbound pinned to an interface
 * (there has to be an interface that can go down). */
export const outboundSupportsDirectFallback = (o: { type?: unknown; bind_interface?: unknown }): boolean =>
  isLeafOutboundType(o.type) && hasBindInterface(o.bind_interface);

const OTHER_MATCHERS = [
  "auth_user",
  "source_ip_cidr",
  "source_port",
  "source_port_range",
  "domain",
  "domain_suffix",
  "domain_keyword",
  "domain_regex",
  "ip_cidr",
  "port",
  "port_range",
  "network",
  "protocol",
] as const;

/** One short "field: a, b (+n)" string per matcher the rule uses besides
 * inbound/inbound_port, for the rule summary. Field names are the JSON keys on
 * purpose - they are what the admin sees in the code view. */
export const otherMatcherSummary = (r: RoutingRule, maxValues = 3): string[] => {
  const parts: string[] = [];
  for (const key of OTHER_MATCHERS) {
    const values = r[key] as ReadonlyArray<string | number> | undefined;
    if (!Array.isArray(values) || values.length === 0) continue;
    const shown = values.slice(0, maxValues).join(", ");
    const hidden = values.length - maxValues;
    parts.push(hidden > 0 ? `${key}: ${shown} (+${hidden})` : `${key}: ${shown}`);
  }
  if (r.source_ip_is_private) parts.push("source_ip_is_private");
  if (r.ip_is_private) parts.push("ip_is_private");
  return parts;
};

/** The 1-based line of each top-level key, for jumping the editor to a section. A scan rather
 * than a parse, so it keeps working while the document is mid-edit and invalid; a JSON string
 * can't span lines, so one left open only costs the rest of its own line. */
export const topLevelKeyLines = (text: string): Record<string, number> => {
  const found: Record<string, number> = {};
  let depth = 0;
  let line = 1;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === "\n") line++;
    else if (c === "{" || c === "[") depth++;
    else if (c === "}" || c === "]") depth--;
    else if (c === '"') {
      let end = i + 1;
      while (end < text.length && text[end] !== '"' && text[end] !== "\n") {
        end += text[end] === "\\" && text[end + 1] !== "\n" ? 2 : 1;
      }
      if (text[end] !== '"') {
        i = end - 1;
        continue;
      }
      const key = text.slice(i + 1, end);
      if (depth === 1 && !(key in found) && /^\s*:/.test(text.slice(end + 1, end + 40))) found[key] = line;
      i = end;
    }
  }
  return found;
};

const isIPv4 = (s: string) =>
  /^\d{1,3}(\.\d{1,3}){3}$/.test(s) && s.split(".").every((p) => Number(p) <= 255 && (p === "0" || !p.startsWith("0")));

const isIPv6 = (s: string) => {
  if (!s.includes(":") || s.includes("[") || s.includes("]") || s.includes("/")) return false;
  try {
    return new URL(`http://[${s}]`).hostname !== "";
  } catch {
    return false;
  }
};

/** A bare address or a CIDR, the two forms sing-box's ip_cidr matcher takes. */
export const isIpOrCidr = (value: string): boolean => {
  const parts = value.split("/");
  if (parts.length > 2) return false;
  const v4 = isIPv4(parts[0]);
  if (!v4 && !isIPv6(parts[0])) return false;
  if (parts.length === 1) return true;
  return /^\d{1,3}$/.test(parts[1]) && Number(parts[1]) <= (v4 ? 32 : 128);
};

/** "start:end", where either side may be left open but not both. */
export const isPortRange = (value: string): boolean => {
  const m = /^(\d*):(\d*)$/.exec(value);
  if (!m || (m[1] === "" && m[2] === "")) return false;
  const start = m[1] === "" ? 0 : Number(m[1]);
  const end = m[2] === "" ? MAX_PORT : Number(m[2]);
  return start <= MAX_PORT && end <= MAX_PORT && start <= end;
};

const isHttpUrl = (value: string): boolean => {
  try {
    const url = new URL(value);
    return (url.protocol === "http:" || url.protocol === "https:") && url.hostname !== "";
  } catch {
    return false;
  }
};

/** A Go duration above zero, such as 3m or 1h30m: what sing-box's interval takes. */
export const isPositiveDuration = (value: string): boolean =>
  /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/.test(value) && /[1-9]/.test(value);

export type ConfigIssueKind =
  | "torExecutablePath"
  | "torDataDirectory"
  | "logLevelInvalid"
  | "notAnObject"
  | "tagRequired"
  | "tagReserved"
  | "tagDuplicate"
  | "outboundTypeInvalid"
  | "serverRequired"
  | "serverPortRequired"
  | "serverPortInvalid"
  | "passwordRequired"
  | "uuidRequired"
  | "usernameRequired"
  | "pskRequired"
  | "methodInvalid"
  | "vmessSecurityInvalid"
  | "flowInvalid"
  | "congestionInvalid"
  | "mbpsRequired"
  | "shadowtlsVersionInvalid"
  | "obfsModeInvalid"
  | "membersRequired"
  | "memberUnknown"
  | "groupUrlInvalid"
  | "groupIntervalInvalid"
  | "groupToleranceInvalid"
  | "groupDefaultInvalid"
  | "dependencyCycle"
  | "directFallbackNotBoolean"
  | "directFallbackNeedsInterface"
  | "directFallbackWrongType"
  | "ruleOutboundRequired"
  | "ruleOutboundUnknown"
  | "ruleInboundUnknown"
  | "ruleNetworkInvalid"
  | "ruleProtocolInvalid"
  | "rulePortInvalid"
  | "ruleIpInvalid"
  | "rulePortRangeInvalid"
  | "ruleUserEmpty"
  | "ruleBlocksEverything"
  | "inboundPortNotList"
  | "inboundPortNeedsInbound"
  | "inboundPortNotInteger"
  | "inboundPortOutOfRange"
  | "inboundPortDuplicate"
  | "dnsTypeInvalid"
  | "dnsAddressRequired"
  | "dnsPortInvalid"
  | "inboundProtocolInvalid"
  | "inboundTlsRequired"
  | "snellPskLength"
  | "snellModeInvalid"
  | "shadowtlsMethodInvalid"
  | "shadowtlsKeyInvalid"
  | "realityKeyInvalid"
  | "realityServerRequired"
  | "realityPortInvalid"
  | "realityShortIdInvalid";

export type IssueScope = "general" | "outbound" | "rule" | "dns" | "inbound";

export type ConfigIssue = {
  scope: IssueScope;
  /** Zero-based position in the section's list. */
  index: number;
  /** The item's own tag, or a rule's outbound_tag, for the message. */
  ref: string;
  kind: ConfigIssueKind;
  /** The form field the problem belongs to, when there is one. */
  field?: string;
  /** The offending value, when the message names it. */
  value?: string;
};

type Reporter = (kind: ConfigIssueKind, field?: string, value?: string) => void;

const reporter = (scope: IssueScope, index: number, ref: string, out: ConfigIssue[]): Reporter =>
  (kind, field, value) => out.push({ scope, index, ref, kind, field, value });

/** Tags of the list at `key`, leaving out the item at `except` (the one being checked or edited). */
export const otherTags = (doc: Item | null, key: "outbounds" | "dns_servers" | "inbounds", except: number | null): Set<string> => {
  const tags = new Set<string>();
  asList(doc?.[key]).forEach((item, i) => {
    if (i !== except && isObject(item) && typeof item.tag === "string" && item.tag !== "") tags.add(item.tag);
  });
  return tags;
};

/** What a routing rule or a balancer may point at: every outbound plus the two built-ins. */
export const outboundTargets = (doc: Item | null): string[] => [
  ...IMPLICIT_OUTBOUND_TAGS,
  ...otherTags(doc, "outbounds", null),
];

const checkDirectFallback = (o: Item, add: Reporter) => {
  if (o.direct_fallback === undefined || o.direct_fallback === null) return;
  if (typeof o.direct_fallback !== "boolean") {
    add("directFallbackNotBoolean", "direct_fallback");
    return;
  }
  if (!o.direct_fallback) return;
  if (!isLeafOutboundType(o.type)) add("directFallbackWrongType", "direct_fallback", str(o.type));
  else if (!hasBindInterface(o.bind_interface)) add("directFallbackNeedsInterface", "bind_interface");
};

export const outboundIssues = (o: Item, index: number, others: ReadonlySet<string>, targets: readonly string[]): ConfigIssue[] => {
  const out: ConfigIssue[] = [];
  const tag = str(o.tag);
  const type = str(o.type);
  const add = reporter("outbound", index, tag, out);

  if (tag === "") add("tagRequired", "tag");
  else if (within(IMPLICIT_OUTBOUND_TAGS, tag)) add("tagReserved", "tag", tag);
  else if (others.has(tag)) add("tagDuplicate", "tag", tag);
  if (!within(OUTBOUND_TYPES, type)) {
    add("outboundTypeInvalid", "type", type);
    return out;
  }
  if (o.server_port !== undefined && o.server_port !== 0 && !isPortNumber(o.server_port)) {
    add("serverPortInvalid", "server_port", String(o.server_port));
  }
  if (str(o.executable_path) !== "") add("torExecutablePath", "executable_path");
  if (str(o.data_directory) !== "" && !/^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$/.test(str(o.data_directory))) {
    add("torDataDirectory", "data_directory", str(o.data_directory));
  }
  if (NEEDS_SERVER_TYPES.has(type)) {
    if (str(o.server) === "") add("serverRequired", "server");
    if (o.server_port === undefined || o.server_port === 0) add("serverPortRequired", "server_port");
  }
  if (within(OUTBOUND_GROUP_TYPES, type)) {
    const members = asStrings(o.outbounds);
    if (members.length === 0) add("membersRequired", "outbounds");
    for (const member of members) if (!targets.includes(member)) add("memberUnknown", "outbounds", member);
    if (type === "urltest") {
      if (str(o.url) !== "" && !isHttpUrl(str(o.url))) add("groupUrlInvalid", "url", str(o.url));
      if (str(o.interval) !== "" && !isPositiveDuration(str(o.interval))) add("groupIntervalInvalid", "interval", str(o.interval));
      if (o.tolerance !== undefined && !(isWhole(o.tolerance) && o.tolerance >= 0 && o.tolerance <= MAX_PORT)) {
        add("groupToleranceInvalid", "tolerance", String(o.tolerance));
      }
    }
    if (type === "selector" && str(o.default) !== "" && !members.includes(str(o.default))) {
      add("groupDefaultInvalid", "default", str(o.default));
    }
  }
  checkDirectFallback(o, add);

  const needPassword = () => {
    if (str(o.password) === "") add("passwordRequired", "password");
  };
  const needUuid = () => {
    if (str(o.uuid) === "") add("uuidRequired", "uuid");
  };
  switch (type) {
    case "shadowsocks":
      if (!within(SHADOWSOCKS_METHODS, o.method)) add("methodInvalid", "method", str(o.method));
      needPassword();
      break;
    case "vmess":
      needUuid();
      if (!within(VMESS_SECURITY_TYPES, o.security)) add("vmessSecurityInvalid", "security", str(o.security));
      break;
    case "vless":
      needUuid();
      if (str(o.flow) !== "" && !within(VLESS_FLOWS, o.flow)) add("flowInvalid", "flow", str(o.flow));
      break;
    case "trojan":
    case "hysteria2":
    case "anytls":
      needPassword();
      break;
    case "tuic":
      needUuid();
      needPassword();
      if (str(o.congestion_control) !== "" && !within(CONGESTION_CONTROL_TYPES, o.congestion_control)) {
        add("congestionInvalid", "congestion_control", str(o.congestion_control));
      }
      break;
    case "hysteria":
      if (!(typeof o.up_mbps === "number" && o.up_mbps > 0)) add("mbpsRequired", "up_mbps");
      if (!(typeof o.down_mbps === "number" && o.down_mbps > 0)) add("mbpsRequired", "down_mbps");
      break;
    case "shadowtls":
      if (!(isWhole(o.version) && o.version >= 1 && o.version <= 3)) add("shadowtlsVersionInvalid", "version");
      else if (o.version !== 1) needPassword();
      break;
    case "snell":
      if (str(o.psk) === "") add("pskRequired", "psk");
      if (str(o.obfs_mode) !== "" && !within(SNELL_OBFS_MODES, o.obfs_mode)) add("obfsModeInvalid", "obfs_mode", str(o.obfs_mode));
      break;
    case "ssh":
      if (str(o.username) === "") add("usernameRequired", "username");
      needPassword();
      break;
  }
  return out;
};

const RULE_CONDITION_KEYS = [
  "inbound", "inbound_port", "domain", "domain_suffix", "domain_keyword", "domain_regex", "ip_cidr", "ip_is_private",
  "port", "port_range", "network", "protocol", "source_ip_cidr", "source_ip_is_private", "source_port", "source_port_range", "auth_user",
];

const ruleHasCondition = (r: Item): boolean =>
  RULE_CONDITION_KEYS.some((key) => (Array.isArray(r[key]) ? r[key].length > 0 : r[key] === true));

/** What a rule sends to when it means block: the built-in, and any block outbound. */
export const blockOutboundTags = (doc: Item | null): ReadonlySet<string> =>
  new Set([
    "block",
    ...asList(doc?.outbounds).flatMap((o) => (isObject(o) && o.type === "block" ? [str(o.tag)] : [])),
  ]);

export const ruleIssues = (
  r: Item,
  index: number,
  targets: readonly string[],
  inboundTags: ReadonlySet<string>,
  blockTags: ReadonlySet<string>
): ConfigIssue[] => {
  const out: ConfigIssue[] = [];
  const target = str(r.outbound_tag);
  const add = reporter("rule", index, target, out);

  if (target === "") add("ruleOutboundRequired", "outbound_tag");
  else if (!targets.includes(target)) add("ruleOutboundUnknown", "outbound_tag", target);
  for (const tag of asStrings(r.inbound)) if (!inboundTags.has(tag)) add("ruleInboundUnknown", "inbound", tag);
  for (const n of asList(r.network)) if (!within(NETWORK_TYPES, n)) add("ruleNetworkInvalid", "network", String(n));
  for (const p of asList(r.protocol)) if (!within(PROTOCOL_TYPES, p)) add("ruleProtocolInvalid", "protocol", String(p));
  for (const key of ["port", "source_port"]) {
    for (const p of asList(r[key])) if (!isPortNumber(p)) add("rulePortInvalid", key, String(p));
  }
  for (const key of ["ip_cidr", "source_ip_cidr"]) {
    for (const ip of asList(r[key])) if (typeof ip !== "string" || !isIpOrCidr(ip)) add("ruleIpInvalid", key, String(ip));
  }
  for (const key of ["port_range", "source_port_range"]) {
    for (const range of asList(r[key])) {
      if (typeof range !== "string" || !isPortRange(range)) add("rulePortRangeInvalid", key, String(range));
    }
  }
  if (asList(r.auth_user).some((user) => user === "")) add("ruleUserEmpty", "auth_user");
  if (blockTags.has(target) && !ruleHasCondition(r)) add("ruleBlocksEverything", "outbound_tag", target);

  const problem = validateInboundPortField(r.inbound_port, r.inbound);
  if (problem) {
    const kind: ConfigIssueKind =
      problem.kind === "notList"
        ? "inboundPortNotList"
        : problem.kind === "needsInbound"
        ? "inboundPortNeedsInbound"
        : problem.kind === "notInteger"
        ? "inboundPortNotInteger"
        : problem.kind === "outOfRange"
        ? "inboundPortOutOfRange"
        : "inboundPortDuplicate";
    add(kind, "inbound_port", "value" in problem ? problem.value : undefined);
  }
  return out;
};

export const dnsIssues = (s: Item, index: number, others: ReadonlySet<string>): ConfigIssue[] => {
  const out: ConfigIssue[] = [];
  const tag = str(s.tag);
  const add = reporter("dns", index, tag, out);
  if (tag === "") add("tagRequired", "tag");
  else if (others.has(tag)) add("tagDuplicate", "tag", tag);
  if (!within(DNS_SERVER_TYPES, s.type)) add("dnsTypeInvalid", "type", str(s.type));
  else if (s.type !== "local" && str(s.address) === "") add("dnsAddressRequired", "address");
  if (s.port !== undefined && s.port !== 0 && !isPortNumber(s.port)) add("dnsPortInvalid", "port", String(s.port));
  return out;
};

const byteLength = (s: string) => new TextEncoder().encode(s).length;

const base64Bytes = (s: string): number | null => {
  if (s === "" || !/^[A-Za-z0-9+/]+={0,2}$/.test(s) || s.length % 4 !== 0) return null;
  return (s.length / 4) * 3 - (s.endsWith("==") ? 2 : s.endsWith("=") ? 1 : 0);
};

export const inboundIssues = (i: Item, index: number, others: ReadonlySet<string>): ConfigIssue[] => {
  const out: ConfigIssue[] = [];
  const tag = str(i.tag);
  const protocol = str(i.protocol);
  const add = reporter("inbound", index, tag, out);
  if (tag === "") add("tagRequired", "tag");
  else if (others.has(tag)) add("tagDuplicate", "tag", tag);
  if (!within(INBOUND_PROTOCOLS, protocol)) {
    add("inboundProtocolInvalid", "protocol", protocol);
    return out;
  }
  if (within(TLS_REQUIRED_PROTOCOLS, protocol) && i.security !== "tls") add("inboundTlsRequired", "security", protocol);
  if (i.security === "reality") {
    if (!/^[A-Za-z0-9_-]{43}$/.test(str(i.reality_private_key))) add("realityKeyInvalid", "reality_private_key");
    if (str(i.reality_server_name) === "") add("realityServerRequired", "reality_server_name");
    if (i.reality_server_port !== undefined && i.reality_server_port !== 0 && !isPortNumber(i.reality_server_port)) {
      add("realityPortInvalid", "reality_server_port", String(i.reality_server_port));
    }
    for (const id of asList(i.reality_short_ids)) {
      if (typeof id !== "string" || !/^([0-9a-fA-F]{2}){0,8}$/.test(id)) add("realityShortIdInvalid", "reality_short_ids", String(id));
    }
  }
  if (protocol === "tuic" && str(i.congestion_control) !== "" && !within(CONGESTION_CONTROL_TYPES, i.congestion_control)) {
    add("congestionInvalid", "congestion_control", str(i.congestion_control));
  }
  if (protocol === "hysteria") {
    if (!(typeof i.up_mbps === "number" && i.up_mbps > 0)) add("mbpsRequired", "up_mbps");
    if (!(typeof i.down_mbps === "number" && i.down_mbps > 0)) add("mbpsRequired", "down_mbps");
  }
  if (protocol === "snell") {
    const length = byteLength(str(i.snell_psk));
    if (length < 12 || length > 255) add("snellPskLength", "snell_psk");
    if (str(i.snell_v6_mode) !== "" && !within(SNELL_V6_MODES, i.snell_v6_mode)) add("snellModeInvalid", "snell_v6_mode", str(i.snell_v6_mode));
  }
  if (protocol === "shadowtls") {
    const need = SHADOWTLS_INNER_KEY_BYTES[str(i.shadowtls_inner_method)];
    if (need === undefined) {
      add("shadowtlsMethodInvalid", "shadowtls_inner_method", str(i.shadowtls_inner_method));
    } else {
      const have = base64Bytes(str(i.shadowtls_inner_password));
      if (have === null || have < need) add("shadowtlsKeyInvalid", "shadowtls_inner_password", String(need));
    }
  }
  return out;
};

const firstCycleMember = (outbounds: unknown[]): string | null => {
  const edges = new Map<string, string[]>();
  for (const o of outbounds) if (isObject(o) && str(o.tag) !== "") edges.set(str(o.tag), asStrings(o.outbounds));
  const visiting = new Set<string>();
  const done = new Set<string>();
  const visit = (tag: string): boolean => {
    if (visiting.has(tag)) return true;
    if (done.has(tag)) return false;
    visiting.add(tag);
    for (const member of edges.get(tag) ?? []) if (visit(member)) return true;
    visiting.delete(tag);
    done.add(tag);
    return false;
  };
  for (const tag of edges.keys()) if (visit(tag)) return tag;
  return null;
};

/** Everything the server would refuse about the document as a whole. */
export const validateCoreConfigDoc = (doc: unknown): ConfigIssue[] => {
  const issues: ConfigIssue[] = [];
  if (!isObject(doc)) return issues;

  if (typeof doc.log_level === "string" && doc.log_level !== "" && !within(LOG_LEVELS, doc.log_level)) {
    issues.push({ scope: "general", index: 0, ref: "", kind: "logLevelInvalid", field: "log_level", value: doc.log_level });
  }

  const targets = outboundTargets(doc);
  const inboundTags = otherTags(doc, "inbounds", null);

  const each = (key: string, scope: IssueScope, check: (item: Item, index: number) => ConfigIssue[]) => {
    asList(doc[key]).forEach((item, index) => {
      if (isObject(item)) issues.push(...check(item, index));
      else issues.push({ scope, index, ref: "", kind: "notAnObject" });
    });
  };
  each("outbounds", "outbound", (o, i) => outboundIssues(o, i, otherTags(doc, "outbounds", i), targets));
  const cycle = firstCycleMember(asList(doc.outbounds));
  if (cycle !== null) {
    const index = asList(doc.outbounds).findIndex((o) => isObject(o) && o.tag === cycle);
    issues.push({ scope: "outbound", index, ref: cycle, kind: "dependencyCycle", field: "outbounds" });
  }
  const blockTags = blockOutboundTags(isObject(doc) ? doc : null);
  each("routing_rules", "rule", (r, i) => ruleIssues(r, i, targets, inboundTags, blockTags));
  each("dns_servers", "dns", (s, i) => dnsIssues(s, i, otherTags(doc, "dns_servers", i)));
  each("inbounds", "inbound", (b, i) => inboundIssues(b, i, otherTags(doc, "inbounds", i)));
  return issues;
};

/** The base itself while it is free, otherwise the base with the first free number. */
export const uniqueTag = (base: string, taken: ReadonlySet<string>): string => {
  if (!taken.has(base) && !within(IMPLICIT_OUTBOUND_TAGS, base)) return base;
  for (let n = 2; ; n++) {
    const candidate = `${base}-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
};

export const moveItem = <T,>(list: readonly T[], from: number, to: number): T[] => {
  const next = [...list];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
};

export type OutboundReferences = { rules: number[]; groups: string[] };

export const outboundReferences = (doc: Item | null, tag: string): OutboundReferences => ({
  rules: asList(doc?.routing_rules).flatMap((r, i) => (isObject(r) && r.outbound_tag === tag ? [i] : [])),
  groups: asList(doc?.outbounds).flatMap((o) =>
    isObject(o) && o.tag !== tag && asStrings(o.outbounds).includes(tag) ? [str(o.tag)] : []
  ),
});

export const inboundReferences = (doc: Item | null, tag: string): number[] =>
  asList(doc?.routing_rules).flatMap((r, i) => (isObject(r) && asStrings(r.inbound).includes(tag) ? [i] : []));

/** Follows a renamed outbound through everything that points at it, so a rename doesn't strand them. */
export const renameOutboundReferences = (doc: Item, from: string, to: string): Item => ({
  ...doc,
  routing_rules: asList(doc.routing_rules).map((r) => (isObject(r) && r.outbound_tag === from ? { ...r, outbound_tag: to } : r)),
  outbounds: asList(doc.outbounds).map((o) =>
    isObject(o) && asStrings(o.outbounds).includes(from)
      ? { ...o, outbounds: asStrings(o.outbounds).map((m) => (m === from ? to : m)) }
      : o
  ),
});

export const renameInboundReferences = (doc: Item, from: string, to: string): Item => ({
  ...doc,
  routing_rules: asList(doc.routing_rules).map((r) =>
    isObject(r) && asStrings(r.inbound).includes(from)
      ? { ...r, inbound: asStrings(r.inbound).map((tag) => (tag === from ? to : tag)) }
      : r
  ),
});
