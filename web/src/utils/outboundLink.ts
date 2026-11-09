import { SHADOWSOCKS_METHODS, TLS_FINGERPRINTS, VMESS_SECURITY_TYPES } from "types/CoreConfig";
import { Item, isEchConfigList, isObject, isRealityPublicKey, isRealityShortId } from "./coreConfigRules";

// Turns a client share link (vless://, vmess://, trojan://, ss://, hysteria2:// or hy2://) into a
// plain outbound in this panel's own core-config shape: the fields of outboundDTO in
// internal/httpapi/coreconfig.go and nothing else. That shape dials TCP or QUIC with TLS or
// REALITY (server name, fingerprint, ALPN, ECH, skip verification) and, for vless/vmess/trojan, a
// ws, grpc, httpupgrade or h2 transport. A link that needs more - xhttp, kcp, quic, VLESS
// encryption, a plugin, obfuscation, a pinned certificate, port hopping - is refused with the
// reason rather than turned into an outbound that would connect differently or not at all. A
// setting the outbound can do without is left out: named in `ignored`, or explained in `notes`
// when what it means needs saying, so the dialog can say what was dropped.

/** Each has its message under rapido.coreForm.link.error. */
export const LINK_ERROR_KINDS = [
  "empty",
  "notLink",
  "scheme",
  "encoding",
  "base64",
  "address",
  "ipv6",
  "noPort",
  "port",
  "portHopping",
  "noId",
  "noPassword",
  "vmessJson",
  "vmessVersion",
  "alterId",
  "vmessSecurity",
  "encryption",
  "flow",
  "security",
  "realityKey",
  "realityShortId",
  "realityServerName",
  "transport",
  "header",
  "ssFormat",
  "method",
  "plugin",
  "obfs",
  "pin",
] as const;
export type LinkErrorKind = (typeof LINK_ERROR_KINDS)[number];

/** Why a link can't be imported. `value` is the offending setting, never a secret from the link. */
export type LinkError = { kind: LinkErrorKind; value?: string };

/** What was left out of an outbound, and why, beyond "it has no field for it". Each has its message
 * under rapido.coreForm.link.note. */
export const LINK_NOTE_KINDS = ["spiderX", "echNotConfig", "grpcMulti", "realityAlpn"] as const;
export type LinkNoteKind = (typeof LINK_NOTE_KINDS)[number];

export type ImportedOutbound = {
  /** The outbound without a tag: the dialog keeps its own. */
  outbound: Item;
  /** The link's own name (its #fragment, or vmess's "ps"), decoded. "" when it has none. */
  name: string;
  /** Settings the link has that the outbound has no field for, by their name in the link. */
  ignored: string[];
  /** Settings left out for a reason of their own. */
  notes: LinkNoteKind[];
};

export type LinkResult = { ok: true; value: ImportedOutbound } | { ok: false; error: LinkError };

class LinkProblem extends Error {
  constructor(readonly problem: LinkError) {
    super(problem.kind);
  }
}

function fail(kind: LinkErrorKind, value?: string): never {
  throw new LinkProblem(value === undefined ? { kind } : { kind, value });
}

/** A JSON or query value as text: vmess links write numbers and booleans either way. */
const text = (v: unknown): string =>
  typeof v === "string" ? v.trim() : typeof v === "number" || typeof v === "boolean" ? String(v) : "";

const truthy = (v: string) => v === "1" || v.toLowerCase() === "true";

const decode = (s: string): string => {
  try {
    return decodeURIComponent(s);
  } catch {
    return fail("encoding");
  }
};

/** A link's settings by name, in any case. What is read is remembered, so what was set and never
 * read can be reported as left out. Names asked for are lower case. */
class Settings {
  private readonly values = new Map<string, { key: string; value: string }>();
  private readonly read = new Set<string>();

  constructor(pairs: Iterable<[string, unknown]>) {
    for (const [key, raw] of pairs) {
      const name = key.toLowerCase();
      if (!this.values.has(name)) this.values.set(name, { key, value: text(raw) });
    }
  }

  /** The value without reading it: it still counts as left out unless something else reads it. */
  peek(name: string): string {
    return this.values.get(name)?.value ?? "";
  }

  /** The first of these that is set; "" when none is. */
  get(...names: string[]): string {
    this.moot(...names);
    return names.map((name) => this.peek(name)).find((value) => value !== "") ?? "";
  }

  /** These no longer count as left out: they mean nothing for this link. */
  moot(...names: string[]): void {
    for (const name of names) this.read.add(name);
  }

  unread(): string[] {
    return [...this.values.entries()].filter(([name, { value }]) => value !== "" && !this.read.has(name)).map(([, { key }]) => key);
  }
}

type LinkParts = {
  /** Everything before the query and the name. */
  head: string;
  /** What comes before the last "@" of the head; null when it has none. */
  userinfo: string | null;
  /** host[:port], without any path. */
  authority: string;
  settings: Settings;
  name: string;
};

const linkName = (fragment: string): string => {
  try {
    return decodeURIComponent(fragment).trim();
  } catch {
    return fragment.trim();
  }
};

// Split by hand rather than with URL: these schemes aren't "special" to the URL parser, which then
// treats the host as opaque, and a password may hold characters it would trip on.
const splitLink = (body: string): LinkParts => {
  const hash = body.indexOf("#");
  const beforeHash = hash >= 0 ? body.slice(0, hash) : body;
  const question = beforeHash.indexOf("?");
  const head = question >= 0 ? beforeHash.slice(0, question) : beforeHash;
  const at = head.lastIndexOf("@");
  const afterAt = head.slice(at + 1);
  const slash = afterAt.indexOf("/");
  return {
    head,
    userinfo: at >= 0 ? head.slice(0, at) : null,
    authority: slash >= 0 ? afterAt.slice(0, slash) : afterAt,
    settings: new Settings(new URLSearchParams(question >= 0 ? beforeHash.slice(question + 1) : "")),
    name: hash >= 0 ? linkName(body.slice(hash + 1)) : "",
  };
};

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
const HOSTNAME = /^[\p{L}\p{N}._-]+$/u;

export const isIPv6 = (s: string): boolean => {
  if (!/^[0-9a-f:.]+$/i.test(s) || !s.includes(":")) return false;
  const halves = s.split("::");
  if (halves.length > 2) return false;
  const groups = halves.flatMap((half) => (half === "" ? [] : half.split(":")));
  let count = groups.length;
  if (count > 0 && groups[count - 1].includes(".")) {
    // An IPv4 address at the end stands for the last two groups.
    if (!IPV4.test(groups.pop() as string)) return false;
    count += 1;
  }
  if (!groups.every((g) => /^[0-9a-f]{1,4}$/i.test(g))) return false;
  return halves.length === 2 ? count < 8 : count === 8;
};

/** The server field: a name, an IPv4 address or a bare IPv6 address (sing-box takes it unbracketed). */
const serverHost = (raw: string): string => {
  const bracketed = raw.startsWith("[") && raw.endsWith("]");
  const host = bracketed ? raw.slice(1, -1) : raw;
  if (bracketed || host.includes(":")) {
    if (!isIPv6(host)) return fail("address");
    return host.toLowerCase();
  }
  if (!HOSTNAME.test(host)) return fail("address");
  return host;
};

const portNumber = (raw: string): number => {
  if (raw === "") return fail("noPort");
  const port = /^\d{1,5}$/.test(raw) ? Number(raw) : 0;
  if (port < 1 || port > 65535) return fail("port", raw);
  return port;
};

type Address = { server: string; server_port: number };

/** host:port, with an IPv6 host in brackets. `defaultPort` is for hysteria2, which has one and may
 * list several ports to hop between. */
const serverAddress = (authority: string, defaultPort?: number): Address => {
  let host = authority;
  let port: string | null = null;
  if (authority.startsWith("[")) {
    const close = authority.indexOf("]");
    if (close < 0) return fail("address");
    host = authority.slice(0, close + 1);
    const after = authority.slice(close + 1);
    if (after !== "" && !after.startsWith(":")) return fail("address");
    if (after !== "") port = after.slice(1);
  } else {
    const colon = authority.indexOf(":");
    if (colon >= 0 && authority.includes(":", colon + 1)) return fail("ipv6");
    if (colon >= 0) {
      host = authority.slice(0, colon);
      port = authority.slice(colon + 1);
    }
  }
  const server = serverHost(host);
  if (port === null && defaultPort !== undefined) return { server, server_port: defaultPort };
  if (defaultPort !== undefined && port !== null && /^[\d,-]+$/.test(port) && /[,-]/.test(port)) return fail("portHopping", port);
  return { server, server_port: portNumber(port ?? "") };
};

const fromBase64 = (encoded: string): string => {
  const body = encoded.replace(/\s+/g, "").replace(/-/g, "+").replace(/_/g, "/").replace(/=+$/, "");
  if (body === "" || !/^[A-Za-z0-9+/]+$/.test(body) || body.length % 4 === 1) return fail("base64");
  let binary: string;
  try {
    binary = atob(body + "=".repeat((4 - (body.length % 4)) % 4));
  } catch {
    return fail("base64");
  }
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(Uint8Array.from(binary, (c) => c.charCodeAt(0)));
  } catch {
    return fail("base64");
  }
};

/** Where a link writes its transport and what belongs to it: a vless or trojan link's query, or
 * vmess's v2 JSON, which keeps the gRPC service name in "path" and the gRPC mode in "type". */
type TransportNames = { network: string; header: string; service: string[]; mode: string };
const QUERY_NAMES: TransportNames = { network: "type", header: "headertype", service: ["servicename"], mode: "mode" };
const VMESS_NAMES: TransportNames = { network: "net", header: "type", service: ["path", "servicename"], mode: "type" };

/** A path as the transports take it: from the root. */
const rootedPath = (path: string): string => (path === "" || path.startsWith("/") ? path : `/${path}`);

/** The V2Ray transport the link names, or none for plain TCP. An HTTP header disguise over TCP, and
 * every other transport (xhttp, splithttp, kcp, mkcp, quic), is refused. */
const transportOptions = (settings: Settings, names: TransportNames, notes: LinkNoteKind[]): Item => {
  const net = (settings.get(names.network) || "tcp").toLowerCase();
  const pathAndHost = (type: string): Item => {
    // A header type means something over TCP only, a gRPC service name and mode to gRPC only.
    settings.moot(names.header, names.mode, ...names.service.filter((n) => n !== "path"));
    const path = rootedPath(settings.get("path"));
    const host = settings.get("host");
    return { transport_type: type, ...(path !== "" ? { transport_path: path } : {}), ...(host !== "" ? { transport_host: host } : {}) };
  };
  switch (net) {
    case "tcp":
    case "raw": {
      const kind = settings.get(names.header).toLowerCase();
      if (kind !== "" && kind !== "none") fail("header", kind);
      // Only an HTTP header would use these.
      settings.moot("host", "path", ...names.service, names.mode);
      return {};
    }
    case "ws":
      return pathAndHost("ws");
    case "httpupgrade":
      return pathAndHost("httpupgrade");
    case "h2":
    case "http":
      return pathAndHost("http");
    case "grpc": {
      const service = settings.get(...names.service);
      const mode = settings.get(names.mode).toLowerCase();
      if (mode === "multi") notes.push("grpcMulti");
      // gRPC goes by its service name; a header type, path or host means nothing to it.
      settings.moot(names.header, "path", "host");
      return { transport_type: "grpc", ...(service !== "" ? { transport_service_name: service } : {}) };
    }
    default:
      return fail("transport", net);
  }
};

type Security = "none" | "tls" | "reality";

const securityOf = (security: string, byDefault: Security): Security => {
  const value = security.toLowerCase();
  if (value === "") return byDefault;
  if (value === "none" || value === "tls" || value === "reality") return value;
  return fail("security", security);
};

/** What a type's TLS takes. `toggle` is false for the types whose TLS is always on and has no
 * switch; `stream` is true for those that also take a fingerprint, ECH and REALITY. */
type TlsSupport = { toggle: boolean; stream: boolean };

/** The TLS fields. Under REALITY the link's pbk, sid and sni are required, and its spx, which the
 * node has no equivalent for, is left out with a note. A fingerprint the outbound can't imitate,
 * or a type without fingerprints, leaves fp unread, so it is reported as left out. */
const tlsOptions = (settings: Settings, security: Security, support: TlsSupport, notes: LinkNoteKind[]): Item => {
  const serverName = settings.get("sni", "peer");
  const insecure = truthy(settings.get("allowinsecure", "insecure"));
  if (security === "none") {
    // All of this means nothing without TLS.
    settings.moot("fp", "alpn", "ech", "pbk", "sid", "spx", "pqv");
    return {};
  }
  const out: Item = {
    ...(support.toggle ? { tls_enabled: true } : {}),
    ...(serverName !== "" ? { tls_server_name: serverName } : {}),
  };
  const fingerprint = settings.peek("fp").toLowerCase();
  if (fingerprint === "" || fingerprint === "none") settings.moot("fp");
  else if (support.stream && (TLS_FINGERPRINTS as readonly string[]).includes(fingerprint)) {
    settings.moot("fp");
    out.tls_fingerprint = fingerprint;
  }

  if (security === "reality") {
    const publicKey = settings.get("pbk");
    if (!isRealityPublicKey(publicKey)) fail("realityKey");
    const shortId = settings.get("sid");
    // The short id is a credential too: the reason doesn't repeat it.
    if (!isRealityShortId(shortId)) fail("realityShortId");
    if (serverName === "") fail("realityServerName");
    out.reality_public_key = publicKey;
    if (shortId !== "") out.reality_short_id = shortId;
    if (settings.get("spx") !== "") notes.push("spiderX");
    if (settings.get("alpn") !== "") notes.push("realityAlpn");
    // REALITY checks the server by its key: skipping certificate checks means nothing to it.
    settings.moot("allowinsecure", "insecure");
    return out;
  }

  settings.moot("pbk", "sid", "spx", "pqv");
  if (insecure) out.tls_insecure = true;
  const alpn = settings
    .get("alpn")
    .split(",")
    .map((p) => p.trim())
    .filter((p) => p !== "");
  if (alpn.length > 0) out.tls_alpn = alpn;
  if (support.stream) {
    const ech = settings.get("ech");
    if (ech !== "" && isEchConfigList(ech)) out.tls_ech_config = ech;
    else if (ech !== "") notes.push("echNotConfig");
  }
  return out;
};

const credential = (userinfo: string | null, missing: LinkErrorKind): string => {
  const value = userinfo === null ? "" : decode(userinfo);
  return value === "" ? fail(missing) : value;
};

const imported = (outbound: Item, name: string, settings: Settings, notes: LinkNoteKind[] = []): ImportedOutbound => ({
  outbound,
  name,
  ignored: settings.unread(),
  notes,
});

const STREAM: TlsSupport = { toggle: true, stream: true };

const vless = (link: LinkParts): ImportedOutbound => {
  const uuid = credential(link.userinfo, "noId");
  const address = serverAddress(link.authority);
  const s = link.settings;
  const encryption = s.get("encryption");
  if (encryption !== "" && encryption.toLowerCase() !== "none") fail("encryption", encryption);
  const security = securityOf(s.get("security"), "none");
  const notes: LinkNoteKind[] = [];
  const transport = transportOptions(s, QUERY_NAMES, notes);
  const flow = s.get("flow");
  if (flow !== "" && flow !== "xtls-rprx-vision") fail("flow", flow);
  return imported(
    { type: "vless", ...address, uuid, ...(flow !== "" ? { flow } : {}), ...tlsOptions(s, security, STREAM, notes), ...transport },
    link.name,
    s,
    notes
  );
};

const trojan = (link: LinkParts): ImportedOutbound => {
  const password = credential(link.userinfo, "noPassword");
  const address = serverAddress(link.authority);
  const s = link.settings;
  // A trojan link means TLS unless it says otherwise.
  const security = securityOf(s.get("security"), "tls");
  const notes: LinkNoteKind[] = [];
  const transport = transportOptions(s, QUERY_NAMES, notes);
  const flow = s.get("flow");
  if (flow !== "") fail("flow", flow);
  return imported({ type: "trojan", ...address, password, ...tlsOptions(s, security, STREAM, notes), ...transport }, link.name, s, notes);
};

// The second name of each pair is the one sing-box knows; Xray takes both for the same cipher.
const SHADOWSOCKS_ALIASES: Record<string, string> = {
  "chacha20-poly1305": "chacha20-ietf-poly1305",
  "xchacha20-poly1305": "xchacha20-ietf-poly1305",
};

const shadowsocks = (link: LinkParts): ImportedOutbound => {
  let credentials: string;
  let authority: string;
  if (link.userinfo !== null) {
    // SIP002: method:password, as it is or in base64 (which never holds a colon).
    const info = decode(link.userinfo);
    credentials = info.includes(":") ? info : fromBase64(info);
    authority = link.authority;
  } else {
    // The older form: one base64 block of method:password@host:port.
    const whole = fromBase64(decode(link.head));
    const at = whole.lastIndexOf("@");
    if (at < 0) fail("ssFormat");
    credentials = whole.slice(0, at);
    authority = whole.slice(at + 1).trim().replace(/\/+$/, "");
  }
  const colon = credentials.indexOf(":");
  if (colon <= 0) fail("ssFormat");
  const given = credentials.slice(0, colon).toLowerCase();
  const method = SHADOWSOCKS_ALIASES[given] ?? given;
  if (!(SHADOWSOCKS_METHODS as readonly string[]).includes(method)) fail("method", given);
  const password = credentials.slice(colon + 1);
  if (password === "") fail("noPassword");
  const s = link.settings;
  const plugin = s.get("plugin");
  if (plugin !== "") fail("plugin", plugin.split(";")[0]);
  return imported({ type: "shadowsocks", ...serverAddress(authority), method, password }, link.name, s);
};

const hysteria2 = (link: LinkParts): ImportedOutbound => {
  // The whole of the user info is the auth string, "user:pass" included.
  const password = credential(link.userinfo, "noPassword");
  const s = link.settings;
  const hop = s.get("mport");
  if (hop !== "") fail("portHopping", hop);
  const address = serverAddress(link.authority, 443);
  const obfs = s.get("obfs");
  if (obfs !== "" && obfs.toLowerCase() !== "none") fail("obfs", obfs);
  if (s.get("pinsha256") !== "") fail("pin");
  // Its TLS is always on; obfs-password means nothing without obfs.
  s.moot("security", "obfs-password");
  return imported({ type: "hysteria2", ...address, password, ...tlsOptions(s, "tls", { toggle: false, stream: false }, []) }, link.name, s);
};

// vmess://base64 of the "v2" JSON object v2rayN made the de facto standard.
const vmess = (body: string): ImportedOutbound => {
  const hash = body.indexOf("#");
  const encoded = (hash >= 0 ? body.slice(0, hash) : body).split("?")[0];
  let json: unknown;
  try {
    json = JSON.parse(fromBase64(encoded.includes("%") ? decode(encoded) : encoded));
  } catch (e) {
    if (e instanceof LinkProblem) throw e;
    return fail("vmessJson");
  }
  if (!isObject(json)) return fail("vmessJson");
  const s = new Settings(Object.entries(json));
  const version = s.get("v");
  if (version !== "" && version !== "2") fail("vmessVersion", version);
  const host = s.get("add");
  const port = s.get("port");
  const address: Address = { server: host === "" ? fail("address") : serverHost(host), server_port: portNumber(port) };
  const uuid = s.get("id");
  if (uuid === "") fail("noId");
  const alterId = s.get("aid");
  if (alterId !== "" && Number(alterId) !== 0) fail("alterId", alterId);
  const security = (s.get("scy") || "auto").toLowerCase();
  if (!(VMESS_SECURITY_TYPES as readonly string[]).includes(security)) fail("vmessSecurity", security);
  const tlsSecurity = securityOf(s.get("tls"), "none");
  const notes: LinkNoteKind[] = [];
  const transport = transportOptions(s, VMESS_NAMES, notes);
  const name = s.get("ps") || (hash >= 0 ? linkName(body.slice(hash + 1)) : "");
  return imported({ type: "vmess", ...address, uuid, security, ...tlsOptions(s, tlsSecurity, STREAM, notes), ...transport }, name, s, notes);
};

const importLink = (input: string): ImportedOutbound => {
  const link = input.trim();
  if (link === "") fail("empty");
  const match = /^([a-z][a-z0-9+.-]*):\/\//i.exec(link);
  if (!match) return fail("notLink");
  const scheme = match[1].toLowerCase();
  const body = link.slice(match[0].length);
  switch (scheme) {
    case "vmess":
      return vmess(body);
    case "vless":
      return vless(splitLink(body));
    case "trojan":
      return trojan(splitLink(body));
    case "ss":
      return shadowsocks(splitLink(body));
    case "hysteria2":
    case "hy2":
      return hysteria2(splitLink(body));
    default:
      return fail("scheme", scheme);
  }
};

/** The outbound a share link describes, or why it can't be one. Pure: nothing is fetched. */
export const parseOutboundLink = (input: string): LinkResult => {
  try {
    return { ok: true, value: importLink(input) };
  } catch (e) {
    if (e instanceof LinkProblem) return { ok: false, error: e.problem };
    throw e;
  }
};
