import {
  CONGESTION_CONTROL_TYPES,
  DNS_SERVER_TYPES,
  NETWORK_TYPES,
  OUTBOUND_GROUP_TYPES,
  OUTBOUND_TYPES,
  PROTOCOL_TYPES,
  SHADOWSOCKS_METHODS,
  SHADOWTLS_VERSIONS,
  SNELL_OBFS_MODES,
  VLESS_FLOWS,
  VMESS_SECURITY_TYPES,
} from "types/CoreConfig";
import {
  INBOUND_NETWORKS,
  INBOUND_PROTOCOLS,
  INBOUND_SECURITIES,
  SHADOWTLS_INNER_KEY_BYTES,
  SNELL_V6_MODES,
  TLS_REQUIRED_PROTOCOLS,
} from "types/Inbound";
import { Item, NEEDS_SERVER_TYPES, asList, isLeafOutboundType, str } from "./coreConfigRules";

// What each section of the core config looks like as a form: which fields an
// item of a given type has, in what order and of what kind. The forms read
// nothing else, so a field added here shows up in the form, and the code view
// needs no change at all because it edits the same object.

export type FieldKind = "text" | "secret" | "number" | "select" | "bool" | "list" | "ports" | "pem" | "pick";

export type GeneratorKind = "password" | "psk" | "shortId" | "ss2022Key" | "realityKey";

export type ChoiceSource = "outboundTargets" | "balancerMembers" | "inboundTags" | "ownMembers";

export type FieldSpec = {
  key: string;
  kind: FieldKind;
  /** Key of the label, under rapido.coreForm.field., when it isn't the field's own key. */
  label?: string;
  /** Key of the help line, under rapido.coreForm.hint. */
  hint?: string;
  required?: boolean;
  disabled?: boolean;
  options?: readonly (string | number)[];
  /** A select may be left empty, which removes the key. */
  optional?: boolean;
  /** Where a select or a pick gets its choices when they depend on the document. */
  source?: ChoiceSource;
  placeholder?: string;
  min?: number;
  max?: number;
  generate?: GeneratorKind;
  /** Takes the form's whole width. */
  wide?: boolean;
  /** Fields that belong together: a heading goes above the first of them. Key under rapido.coreForm.group. */
  group?: string;
  /** A line under that heading, set on the group's first field. Key under rapido.coreForm.hint. */
  groupHint?: string;
};

const tlsFields = (toggle: boolean, enabled: boolean): FieldSpec[] => {
  const details: FieldSpec[] = [
    { key: "tls_server_name", kind: "text", placeholder: "example.com" },
    { key: "tls_insecure", kind: "bool" },
  ];
  if (!toggle) return details;
  return [{ key: "tls_enabled", kind: "bool", wide: true }, ...(enabled ? details : [])];
};

const password: FieldSpec = { key: "password", kind: "secret", required: true };
const uuid: FieldSpec = { key: "uuid", kind: "secret", required: true, placeholder: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" };

/** `types` limits the type picker: the Outbounds tab and the Balancers tab each offer their own. */
export const outboundFields = (o: Item, types: readonly string[] = OUTBOUND_TYPES): FieldSpec[] => {
  const type = str(o.type);
  const fields: FieldSpec[] = [
    { key: "tag", kind: "text", required: true, hint: "outboundTag", placeholder: "exit-de" },
    { key: "type", kind: "select", required: true, options: types, hint: (OUTBOUND_GROUP_TYPES as readonly string[]).includes(type) ? "groupType" : undefined },
  ];
  if ((OUTBOUND_GROUP_TYPES as readonly string[]).includes(type)) {
    fields.push({ key: "outbounds", kind: "pick", label: "members", required: true, source: "balancerMembers", wide: true });
    if (type === "urltest") {
      fields.push(
        { key: "url", kind: "text", wide: true, placeholder: "https://www.gstatic.com/generate_204", hint: "urltestUrl" },
        { key: "interval", kind: "text", placeholder: "3m", hint: "urltestInterval" },
        { key: "tolerance", kind: "number", min: 0, max: 65535, placeholder: "50", hint: "urltestTolerance" }
      );
    } else {
      fields.push({ key: "default", kind: "select", optional: true, source: "ownMembers", hint: "selectorDefault" });
    }
    return fields;
  }
  if (NEEDS_SERVER_TYPES.has(type)) {
    fields.push(
      { key: "server", kind: "text", required: true, placeholder: "203.0.113.10" },
      { key: "server_port", kind: "number", required: true, min: 1, max: 65535, placeholder: "443" }
    );
  }
  const tlsOn = o.tls_enabled === true;
  switch (type) {
    case "socks":
    case "http":
      fields.push({ key: "username", kind: "text" }, { ...password, required: false });
      break;
    case "ssh":
      fields.push({ key: "username", kind: "text", required: true }, password);
      break;
    case "shadowsocks":
      fields.push({ key: "method", kind: "select", required: true, options: SHADOWSOCKS_METHODS }, password);
      break;
    case "vmess":
      fields.push(uuid, { key: "security", kind: "select", label: "vmessSecurity", required: true, options: VMESS_SECURITY_TYPES }, ...tlsFields(true, tlsOn));
      break;
    case "trojan":
      fields.push(password, ...tlsFields(true, tlsOn));
      break;
    case "vless":
      fields.push(uuid, { key: "flow", kind: "select", optional: true, options: VLESS_FLOWS }, ...tlsFields(true, tlsOn));
      break;
    case "hysteria2":
    case "anytls":
      fields.push(password, ...tlsFields(false, true));
      break;
    case "tuic":
      fields.push(uuid, password, { key: "congestion_control", kind: "select", optional: true, options: CONGESTION_CONTROL_TYPES }, ...tlsFields(false, true));
      break;
    case "hysteria":
      fields.push(
        { ...password, label: "authStr", required: false },
        { key: "obfs", kind: "secret" },
        { key: "up_mbps", kind: "number", required: true, min: 1 },
        { key: "down_mbps", kind: "number", required: true, min: 1 },
        ...tlsFields(false, true)
      );
      break;
    case "shadowtls":
      fields.push(
        { key: "version", kind: "select", required: true, options: SHADOWTLS_VERSIONS },
        { ...password, required: o.version !== 1 },
        ...tlsFields(false, true)
      );
      break;
    case "snell":
      fields.push({ key: "psk", kind: "secret", required: true }, { key: "obfs_mode", kind: "select", optional: true, options: SNELL_OBFS_MODES });
      if (o.obfs_mode === "http" || o.obfs_mode === "tls") fields.push({ key: "obfs_host", kind: "text" });
      break;
    case "tor":
      fields.push({ key: "data_directory", kind: "text", hint: "torDataDirectory" });
      break;
  }
  if (isLeafOutboundType(type)) {
    fields.push({ key: "bind_interface", kind: "text", placeholder: "wg0", hint: "bindInterface" });
    if (str(o.bind_interface).trim() !== "") fields.push({ key: "direct_fallback", kind: "bool", hint: "directFallback", wide: true });
  }
  return fields;
};

export const ruleFields = (r: Item): FieldSpec[] => {
  const hasInbound = asList(r.inbound).length > 0;
  return [
    { key: "outbound_tag", kind: "select", required: true, source: "outboundTargets", wide: true, hint: "ruleOutbound" },
    { key: "inbound", kind: "pick", source: "inboundTags", wide: true, hint: "ruleInbound" },
    ...(hasInbound ? [{ key: "inbound_port", kind: "ports", wide: true, hint: "rulePorts" } as FieldSpec] : []),
    { key: "auth_user", kind: "list", group: "source", groupHint: "needsNodeUpdate", placeholder: "username", hint: "listSeparators" },
    { key: "source_ip_cidr", kind: "list", group: "source", placeholder: "10.0.0.0/8, 1.1.1.1", hint: "listSeparators" },
    { key: "source_port", kind: "ports", group: "source" },
    { key: "source_port_range", kind: "list", group: "source", placeholder: "1000:2000", hint: "listSeparators" },
    { key: "source_ip_is_private", kind: "bool", group: "source", wide: true },
    { key: "domain", kind: "list", group: "destination", placeholder: "example.com", hint: "needsSniff" },
    { key: "domain_suffix", kind: "list", group: "destination", placeholder: ".ir", hint: "needsSniff" },
    { key: "domain_keyword", kind: "list", group: "destination", placeholder: "google", hint: "needsSniff" },
    { key: "domain_regex", kind: "list", group: "destination", placeholder: "^.*\\.ir$", hint: "regexNeedsUpdate" },
    { key: "ip_cidr", kind: "list", group: "destination", placeholder: "10.0.0.0/8, 1.1.1.1", hint: "listSeparators" },
    { key: "port", kind: "ports", group: "destination" },
    { key: "port_range", kind: "list", group: "destination", placeholder: "1000:2000", hint: "listSeparators" },
    { key: "ip_is_private", kind: "bool", group: "destination", wide: true },
    { key: "network", kind: "pick", group: "traffic", options: NETWORK_TYPES },
    { key: "protocol", kind: "pick", group: "traffic", options: PROTOCOL_TYPES, hint: "protocolSniff" },
  ];
};

export const dnsFields = (s: Item): FieldSpec[] => {
  const type = str(s.type);
  const fields: FieldSpec[] = [
    { key: "tag", kind: "text", required: true, placeholder: "dns-remote" },
    { key: "type", kind: "select", required: true, options: DNS_SERVER_TYPES, hint: "dnsType" },
  ];
  if (type === "local") return fields;
  fields.push(
    { key: "address", kind: "text", required: true, placeholder: "1.1.1.1" },
    { key: "port", kind: "number", min: 1, max: 65535, hint: "dnsPort" }
  );
  if (type === "https") fields.push({ key: "path", kind: "text", placeholder: "/dns-query" });
  return fields;
};

const TRANSPORT_PROTOCOLS = new Set(["vless", "vmess", "trojan", "shadowsocks"]);
const FIXED_SECURITY = new Set<string>([...TLS_REQUIRED_PROTOCOLS, "snell", "shadowtls"]);

/** `tagLocked`: an inbound the server already has keeps its tag, because hosts hang off it. */
export const inboundFields = (i: Item, tagLocked: boolean): FieldSpec[] => {
  const protocol = str(i.protocol);
  const fields: FieldSpec[] = [
    { key: "tag", kind: "text", required: true, disabled: tagLocked, hint: tagLocked ? "tagLocked" : undefined, placeholder: "vless-main" },
    { key: "protocol", kind: "select", required: true, options: INBOUND_PROTOCOLS },
  ];
  if (TRANSPORT_PROTOCOLS.has(protocol)) {
    fields.push(
      { key: "network", kind: "select", label: "inboundNetwork", options: INBOUND_NETWORKS },
      { key: "header_type", kind: "text", placeholder: "http" }
    );
  }
  if (!FIXED_SECURITY.has(protocol)) {
    fields.push({ key: "security", kind: "select", options: INBOUND_SECURITIES });
  }
  const security = (TLS_REQUIRED_PROTOCOLS as readonly string[]).includes(protocol) ? "tls" : str(i.security);
  if (security === "reality" && !FIXED_SECURITY.has(protocol)) {
    fields.push(
      { key: "reality_private_key", kind: "secret", generate: "realityKey", wide: true, hint: "realityKey" },
      { key: "reality_short_ids", kind: "list", generate: "shortId" },
      { key: "reality_server_name", kind: "text", placeholder: "www.example.com" },
      { key: "reality_server_port", kind: "number", min: 1, max: 65535, placeholder: "443" }
    );
  }
  if (security === "tls") {
    fields.push(
      { key: "tls_server_name", kind: "text", label: "inboundTlsServerName", placeholder: "example.com" },
      { key: "tls_certificate", kind: "pem", wide: true, hint: "tlsCertificate" },
      { key: "tls_key", kind: "pem", wide: true }
    );
  }
  switch (protocol) {
    case "hysteria2":
      fields.push(
        { key: "hysteria2_obfs_password", kind: "secret", generate: "password" },
        { key: "up_mbps", kind: "number", min: 1 },
        { key: "down_mbps", kind: "number", min: 1 }
      );
      break;
    case "hysteria":
      fields.push(
        { key: "up_mbps", kind: "number", required: true, min: 1 },
        { key: "down_mbps", kind: "number", required: true, min: 1 },
        { key: "hysteria_obfs_password", kind: "secret", generate: "password" }
      );
      break;
    case "tuic":
      fields.push(
        { key: "congestion_control", kind: "select", optional: true, options: CONGESTION_CONTROL_TYPES },
        { key: "zero_rtt_handshake", kind: "bool", wide: true }
      );
      break;
    case "snell":
      fields.push(
        { key: "snell_psk", kind: "secret", required: true, generate: "psk", hint: "snellPsk" },
        { key: "snell_v6_mode", kind: "select", optional: true, options: SNELL_V6_MODES }
      );
      break;
    case "shadowtls":
      fields.push(
        { key: "shadowtls_inner_method", kind: "select", required: true, options: Object.keys(SHADOWTLS_INNER_KEY_BYTES) },
        { key: "shadowtls_inner_password", kind: "secret", required: true, generate: "ss2022Key", hint: "shadowtlsKey" }
      );
      break;
  }
  return fields;
};

// Every key a form can produce for a section. Saving drops the ones the item's
// current type doesn't show (a server left over from before switching a type
// to direct, say), and leaves any key not listed here alone: a field this
// dashboard has never heard of must survive a trip through the form.
const OUTBOUND_KEYS = [
  "server", "server_port", "username", "password", "outbounds", "uuid", "flow", "method", "security",
  "congestion_control", "version", "psk", "obfs_mode", "obfs_host", "obfs", "up_mbps", "down_mbps",
  "executable_path", "data_directory", "tls_enabled", "tls_server_name", "tls_insecure", "bind_interface", "direct_fallback",
  "url", "interval", "tolerance", "default",
];
const RULE_KEYS = ["inbound_port"];
const DNS_KEYS = ["address", "port", "path"];
const INBOUND_KEYS = [
  "network", "header_type", "security", "reality_private_key", "reality_short_ids", "reality_server_name",
  "reality_server_port", "tls_certificate", "tls_key", "tls_server_name", "hysteria2_obfs_password", "up_mbps", "down_mbps",
  "congestion_control", "zero_rtt_handshake", "snell_psk", "snell_v6_mode", "hysteria_obfs_password",
  "shadowtls_inner_method", "shadowtls_inner_password",
];

const pruned = (item: Item, known: readonly string[], fields: readonly FieldSpec[]): Item => {
  const shown = new Set(fields.map((f) => f.key));
  const next = { ...item };
  for (const key of known) if (!shown.has(key)) delete next[key];
  return next;
};

export const normalizeOutbound = (o: Item, types?: readonly string[]): Item => {
  const next = pruned(o, OUTBOUND_KEYS, outboundFields(o, types));
  if (typeof next.bind_interface === "string") {
    const trimmed = next.bind_interface.trim();
    if (trimmed === "") delete next.bind_interface;
    else next.bind_interface = trimmed;
  }
  return next;
};

export const normalizeRule = (r: Item): Item => pruned(r, RULE_KEYS, ruleFields(r));

export const normalizeDns = (s: Item): Item => pruned(s, DNS_KEYS, dnsFields(s));

export const normalizeInbound = (i: Item, tagLocked: boolean): Item => {
  const protocol = str(i.protocol);
  const fixed = (TLS_REQUIRED_PROTOCOLS as readonly string[]).includes(protocol) ? "tls" : FIXED_SECURITY.has(protocol) ? "none" : null;
  const item = fixed ? { ...i, security: fixed } : i;
  const next = pruned(item, INBOUND_KEYS, inboundFields(item, tagLocked));
  if (fixed) next.security = fixed;
  return next;
};

/** What a form fills in by itself when an item's type changes: the one choice nearly everyone
 * wants, for a required field that would otherwise start empty. */
export const outboundDefaults = (o: Item): Item => {
  if (o.type === "vmess" && o.security === undefined) return { ...o, security: "auto" };
  if (o.type === "shadowtls" && o.version === undefined) return { ...o, version: 3 };
  return o;
};

export const inboundDefaults = (i: Item): Item => {
  if (TRANSPORT_PROTOCOLS.has(str(i.protocol)) && i.network === undefined) return { ...i, network: "tcp" };
  if (i.protocol === "shadowtls" && i.shadowtls_inner_method === undefined) {
    return { ...i, shadowtls_inner_method: Object.keys(SHADOWTLS_INNER_KEY_BYTES)[0] };
  }
  return i;
};

export const NEW_OUTBOUND: Item = { tag: "", type: "direct" };
export const NEW_BALANCER: Item = { tag: "", type: "urltest", outbounds: [] };
export const NEW_DNS: Item = { tag: "", type: "udp", address: "" };
export const NEW_INBOUND: Item = { tag: "", protocol: "vless", network: "tcp", security: "none" };

/** What a list field splits on: commas (incl. the Arabic comma), semicolons and whitespace. */
export const splitList = (text: string): string[] => text.split(/[\s,;،؛]+/).filter((token) => token !== "");

export const OUTBOUND_TYPES_PLAIN = OUTBOUND_TYPES.filter((type) => !(OUTBOUND_GROUP_TYPES as readonly string[]).includes(type));
