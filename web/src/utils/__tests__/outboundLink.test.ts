import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import en from "../../../public/statics/locales/en.json";
import fa from "../../../public/statics/locales/fa.json";
import ru from "../../../public/statics/locales/ru.json";
import zh from "../../../public/statics/locales/zh.json";
import { OUTBOUND_TYPES_PLAIN, normalizeOutbound } from "../coreConfigFields";
import { validateCoreConfigDoc } from "../coreConfigRules";
import { ImportedOutbound, LINK_ERROR_KINDS, LINK_NOTE_KINDS, LinkError, isIPv6, parseOutboundLink } from "../outboundLink";

const UUID = "1b0f8d6e-3c2a-4e5f-9a7b-0c1d2e3f4a5b";
// An X25519 public key, and an ECHConfigList for cloudflare-ech.com, both as links carry them.
const PBK = "tNusmlSozuXV95B7bOpp7J_vPBWfCis3SChNyuEL9BA";
const ECH = "AE3+DQBJAAAgACAXs75gt43sJfOCELDz/NA22JSJrfmuEAMbYx9uuadRagAMAAEAAQABAAIAAQADABJjbG91ZGZsYXJlLWVjaC5jb20AAA==";

const ok = (link: string): ImportedOutbound => {
  const result = parseOutboundLink(link);
  if (!result.ok) throw new Error(`refused ${link}: ${JSON.stringify(result.error)}`);
  return result.value;
};

const refused = (link: string): LinkError => {
  const result = parseOutboundLink(link);
  if (result.ok) throw new Error(`accepted ${link}: ${JSON.stringify(result.value)}`);
  return result.error;
};

const utf8Base64 = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)));
const base64Url = (s: string) => utf8Base64(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
const vmessLink = (o: object) => `vmess://${utf8Base64(JSON.stringify(o))}`;

const VMESS = {
  v: "2",
  ps: "آلمان ۱",
  add: "de.example.com",
  port: "443",
  id: UUID,
  aid: "0",
  scy: "auto",
  net: "tcp",
  type: "none",
  host: "",
  path: "",
  tls: "tls",
  sni: "sni.example.com",
  alpn: "",
  fp: "",
};

describe("vless", () => {
  it("takes the id, the address, vision and TLS with its fingerprint and ALPN", () => {
    const got = ok(
      `vless://${UUID}@example.com:443?encryption=none&flow=xtls-rprx-vision&security=tls&sni=sni.example.com&fp=chrome&alpn=h2%2Chttp%2F1.1&type=tcp&headerType=none&host=&path=#%D8%A2%D9%84%D9%85%D8%A7%D9%86%20DE`
    );
    expect(got.outbound).toEqual({
      type: "vless",
      server: "example.com",
      server_port: 443,
      uuid: UUID,
      flow: "xtls-rprx-vision",
      tls_enabled: true,
      tls_server_name: "sni.example.com",
      tls_fingerprint: "chrome",
      tls_alpn: ["h2", "http/1.1"],
    });
    expect(got.name).toBe("آلمان DE");
    expect(got.ignored).toEqual([]);
    expect(got.notes).toEqual([]);
  });

  it("leaves out a fingerprint the outbound can't imitate, by name", () => {
    const got = ok(`vless://${UUID}@h.example:443?security=tls&fp=hellochrome_120`);
    expect(got.outbound).not.toHaveProperty("tls_fingerprint");
    expect(got.ignored).toEqual(["fp"]);
  });

  it("takes an ECH config list, and explains one it can't use", () => {
    expect(ok(`vless://${UUID}@h.example:443?security=tls&sni=i.example&ech=${encodeURIComponent(ECH)}`).outbound.tls_ech_config).toBe(ECH);
    const lookup = ok(`vless://${UUID}@h.example:443?security=tls&ech=cloudflare-ech.com%2Bhttps%3A%2F%2F1.1.1.1%2Fdns-query`);
    expect(lookup.outbound).not.toHaveProperty("tls_ech_config");
    expect(lookup.notes).toEqual(["echNotConfig"]);
    expect(lookup.ignored).toEqual([]);
  });

  it("leaves TLS off without security, and the TLS-only settings then mean nothing", () => {
    const got = ok(`vless://${UUID}@203.0.113.7:8080?type=tcp&security=none&sni=x.example.com&fp=chrome&alpn=h2`);
    expect(got.outbound).toEqual({ type: "vless", server: "203.0.113.7", server_port: 8080, uuid: UUID });
    expect(got.ignored).toEqual([]);
    expect(got.name).toBe("");
  });

  it("skips certificate checks when the link says to, however it spells it", () => {
    expect(ok(`vless://${UUID}@h.example:443?security=tls&allowInsecure=1`).outbound.tls_insecure).toBe(true);
    expect(ok(`vless://${UUID}@h.example:443?security=tls&insecure=true`).outbound.tls_insecure).toBe(true);
    expect(ok(`vless://${UUID}@h.example:443?security=tls&allowInsecure=0`).outbound).not.toHaveProperty("tls_insecure");
  });

  it("reads the scheme and the setting names in any case, and raw as tcp", () => {
    const got = ok(`VLESS://${UUID}@h.example:443?Security=TLS&SNI=s.example&Type=raw&fp=none`);
    expect(got.outbound).toMatchObject({ type: "vless", tls_enabled: true, tls_server_name: "s.example" });
    expect(got.ignored).toEqual([]);
  });

  it("keeps an IPv6 server in brackets apart from its port, and stores it bare", () => {
    const got = ok(`vless://${UUID}@[2001:DB8::1]:8443?security=none`);
    expect(got.outbound).toMatchObject({ server: "2001:db8::1", server_port: 8443 });
    expect(ok(`vless://${UUID}@[::ffff:192.0.2.1]:443`).outbound.server).toBe("::ffff:192.0.2.1");
  });

  it("reports settings it doesn't know rather than dropping them silently", () => {
    expect(ok(`vless://${UUID}@h.example:443?security=tls&packetEncoding=xudp`).ignored).toEqual(["packetEncoding"]);
  });

  it.each([
    ["REALITY with a key that isn't one", `vless://${UUID}@h.example:443?security=reality&pbk=abc&sid=01&sni=www.example.com&fp=chrome`, { kind: "realityKey" }],
    ["REALITY without a key", `vless://${UUID}@h.example:443?security=reality&sni=www.example.com`, { kind: "realityKey" }],
    ["REALITY with an odd short id", `vless://${UUID}@h.example:443?security=reality&pbk=${PBK}&sid=abc&sni=www.example.com`, { kind: "realityShortId" }],
    ["REALITY without sni", `vless://${UUID}@h.example:443?security=reality&pbk=${PBK}&sid=01`, { kind: "realityServerName" }],
    ["another security", `vless://${UUID}@h.example:443?security=xtls`, { kind: "security", value: "xtls" }],
    ["XHTTP", `vless://${UUID}@h.example:443?type=xhttp`, { kind: "transport", value: "xhttp" }],
    ["SplitHTTP", `vless://${UUID}@h.example:443?type=splithttp&path=%2F`, { kind: "transport", value: "splithttp" }],
    ["mKCP", `vless://${UUID}@h.example:443?type=kcp&seed=x`, { kind: "transport", value: "kcp" }],
    ["mkcp by that name", `vless://${UUID}@h.example:443?type=mkcp`, { kind: "transport", value: "mkcp" }],
    ["QUIC", `vless://${UUID}@h.example:443?type=quic`, { kind: "transport", value: "quic" }],
    ["an HTTP header", `vless://${UUID}@h.example:80?type=tcp&headerType=http&host=a.example`, { kind: "header", value: "http" }],
    ["another flow", `vless://${UUID}@h.example:443?security=tls&flow=xtls-rprx-vision-udp443`, { kind: "flow", value: "xtls-rprx-vision-udp443" }],
    ["VLESS encryption", `vless://${UUID}@h.example:443?encryption=mlkem768x25519plus.native.0rtt.abc`, { kind: "encryption", value: "mlkem768x25519plus.native.0rtt.abc" }],
    ["no id", "vless://@h.example:443", { kind: "noId" }],
    ["no user info at all", "vless://h.example:443", { kind: "noId" }],
    ["no port", `vless://${UUID}@h.example`, { kind: "noPort" }],
    ["an empty port", `vless://${UUID}@h.example:`, { kind: "noPort" }],
    ["a port out of range", `vless://${UUID}@h.example:70000`, { kind: "port", value: "70000" }],
    ["port zero", `vless://${UUID}@h.example:0`, { kind: "port", value: "0" }],
    ["a port that isn't a number", `vless://${UUID}@h.example:https`, { kind: "port", value: "https" }],
    ["IPv6 without brackets", `vless://${UUID}@2001:db8::1:443`, { kind: "ipv6" }],
    ["an unclosed bracket", `vless://${UUID}@[2001:db8::1:443`, { kind: "address" }],
    ["text after the bracket", `vless://${UUID}@[2001:db8::1]x443`, { kind: "address" }],
    ["a bracket that isn't IPv6", `vless://${UUID}@[example.com]:443`, { kind: "address" }],
    ["no host", `vless://${UUID}@:443`, { kind: "address" }],
    ["a host with spaces", `vless://${UUID}@my host:443`, { kind: "address" }],
    ["a broken escape", "vless://%E0%A4%A@h.example:443", { kind: "encoding" }],
  ])("refuses %s", (_, link, error) => {
    expect(refused(link)).toEqual(error);
  });
});

describe("REALITY", () => {
  it("takes the key, the short id, the name and the fingerprint, and says what spx was", () => {
    const got = ok(
      `vless://${UUID}@203.0.113.9:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=${PBK}&sid=0123abcd&spx=%2F&type=tcp&headerType=none#NL`
    );
    expect(got.outbound).toEqual({
      type: "vless",
      server: "203.0.113.9",
      server_port: 443,
      uuid: UUID,
      flow: "xtls-rprx-vision",
      tls_enabled: true,
      tls_server_name: "www.example.com",
      tls_fingerprint: "chrome",
      reality_public_key: PBK,
      reality_short_id: "0123abcd",
    });
    expect(got.notes).toEqual(["spiderX"]);
    expect(got.ignored).toEqual([]);
  });

  it("leaves the fingerprint to the default when the link names none, and ALPN and insecure out", () => {
    const got = ok(`vless://${UUID}@h.example:443?security=reality&sni=www.example.com&pbk=${PBK}&alpn=h2&allowInsecure=1`);
    expect(got.outbound).not.toHaveProperty("tls_fingerprint");
    expect(got.outbound).not.toHaveProperty("tls_alpn");
    expect(got.outbound).not.toHaveProperty("tls_insecure");
    expect(got.outbound).not.toHaveProperty("reality_short_id");
    expect(got.notes).toEqual(["realityAlpn"]);
  });

  it("goes under gRPC, and on trojan", () => {
    const got = ok(`trojan://pw@h.example:443?security=reality&sni=www.example.com&pbk=${PBK}&sid=ab&type=grpc&serviceName=svc&mode=multi`);
    expect(got.outbound).toMatchObject({ type: "trojan", reality_public_key: PBK, transport_type: "grpc", transport_service_name: "svc" });
    expect(got.notes).toEqual(["grpcMulti"]);
  });
});

describe("transports", () => {
  it("takes WebSocket with its path and host, under TLS", () => {
    const got = ok(
      `vless://${UUID}@cdn.example.com:443?security=tls&sni=cdn.example.com&alpn=http%2F1.1&fp=firefox&type=ws&path=%2Fray%3Fed%3D2048&host=cdn.example.com&headerType=none`
    );
    expect(got.outbound).toEqual({
      type: "vless",
      server: "cdn.example.com",
      server_port: 443,
      uuid: UUID,
      tls_enabled: true,
      tls_server_name: "cdn.example.com",
      tls_fingerprint: "firefox",
      tls_alpn: ["http/1.1"],
      transport_type: "ws",
      transport_path: "/ray?ed=2048",
      transport_host: "cdn.example.com",
    });
    expect(got.ignored).toEqual([]);
  });

  it("puts a path that leaves out its first slash at the root", () => {
    expect(ok(`vless://${UUID}@h.example:80?type=ws&path=ray`).outbound.transport_path).toBe("/ray");
  });

  it.each([
    ["gRPC", `vless://${UUID}@h.example:443?type=grpc&serviceName=svc&mode=gun`, { transport_type: "grpc", transport_service_name: "svc" }],
    ["HTTPUpgrade", `vless://${UUID}@h.example:80?type=httpupgrade&path=%2Fup&host=h.example`, { transport_type: "httpupgrade", transport_path: "/up", transport_host: "h.example" }],
    ["HTTP/2 as h2", `vless://${UUID}@h.example:443?security=tls&type=h2&path=%2Fh2&host=a.example%2Cb.example`, { transport_type: "http", transport_path: "/h2", transport_host: "a.example,b.example" }],
    ["HTTP/2 as http", `vless://${UUID}@h.example:443?security=tls&type=http&path=%2Fh2`, { transport_type: "http", transport_path: "/h2" }],
    ["gRPC on trojan", "trojan://pw@h.example:443?type=grpc&serviceName=x", { transport_type: "grpc", transport_service_name: "x" }],
  ])("takes %s", (_, link, fields) => {
    const got = ok(link);
    expect(got.outbound).toMatchObject(fields);
    expect(got.ignored).toEqual([]);
  });
});

describe("vmess", () => {
  it("reads the v2 JSON form", () => {
    const got = ok(vmessLink({ ...VMESS, fp: "chrome" }));
    expect(got.outbound).toEqual({
      type: "vmess",
      server: "de.example.com",
      server_port: 443,
      uuid: UUID,
      security: "auto",
      tls_enabled: true,
      tls_server_name: "sni.example.com",
      tls_fingerprint: "chrome",
    });
    expect(got.name).toBe("آلمان ۱");
    expect(got.ignored).toEqual([]);
  });

  it("reads WebSocket, where type means nothing, and gRPC, whose service name is in path", () => {
    const ws = ok(vmessLink({ ...VMESS, net: "ws", path: "/ws", host: "cdn.example.com", type: "none" }));
    expect(ws.outbound).toMatchObject({ transport_type: "ws", transport_path: "/ws", transport_host: "cdn.example.com" });
    expect(ws.ignored).toEqual([]);
    const grpc = ok(vmessLink({ ...VMESS, net: "grpc", path: "svc", type: "multi", host: "ignored.example" }));
    expect(grpc.outbound).toMatchObject({ transport_type: "grpc", transport_service_name: "svc" });
    expect(grpc.outbound).not.toHaveProperty("transport_path");
    expect(grpc.notes).toEqual(["grpcMulti"]);
    expect(grpc.ignored).toEqual([]);
  });

  it("reads REALITY", () => {
    const got = ok(vmessLink({ ...VMESS, tls: "reality", pbk: PBK, sid: "ab", sni: "www.example.com" }));
    expect(got.outbound).toMatchObject({ tls_enabled: true, reality_public_key: PBK, reality_short_id: "ab", tls_server_name: "www.example.com" });
  });

  it("takes numbers as numbers, a missing cipher as auto, and no TLS", () => {
    const got = ok(vmessLink({ add: "203.0.113.9", port: 10086, id: UUID, aid: 0, net: "tcp", tls: "" }));
    expect(got.outbound).toEqual({ type: "vmess", server: "203.0.113.9", server_port: 10086, uuid: UUID, security: "auto" });
  });

  it("keeps the cipher the link names, and skips verification when asked", () => {
    const got = ok(vmessLink({ ...VMESS, scy: "AES-128-GCM", allowInsecure: true }));
    expect(got.outbound).toMatchObject({ security: "aes-128-gcm", tls_insecure: true });
  });

  it("decodes URL-safe base64 without padding, and base64 split over lines", () => {
    // Six of a kind always holds one aligned run of three: "~~~" encodes to "fn5+", "???" to "Pz8/".
    const body = JSON.stringify({ ...VMESS, ps: "~~~~~~??????" });
    expect(base64Url(body)).toMatch(/-.*_|_.*-/);
    expect(ok(`vmess://${base64Url(body)}`).name).toBe("~~~~~~??????");
    const wrapped = utf8Base64(body).replace(/(.{40})/g, "$1\n");
    expect(ok(`vmess://${wrapped}`).outbound.server).toBe("de.example.com");
  });

  it("takes an IPv6 address with or without brackets", () => {
    expect(ok(vmessLink({ ...VMESS, add: "2001:db8::5" })).outbound.server).toBe("2001:db8::5");
    expect(ok(vmessLink({ ...VMESS, add: "[2001:db8::5]" })).outbound.server).toBe("2001:db8::5");
  });

  it.each([
    ["mKCP", { ...VMESS, net: "kcp" }, { kind: "transport", value: "kcp" }],
    ["XHTTP", { ...VMESS, net: "xhttp" }, { kind: "transport", value: "xhttp" }],
    ["an HTTP header", { ...VMESS, type: "http" }, { kind: "header", value: "http" }],
    ["an alterId", { ...VMESS, aid: "64" }, { kind: "alterId", value: "64" }],
    ["an unknown cipher", { ...VMESS, scy: "rc4" }, { kind: "vmessSecurity", value: "rc4" }],
    ["REALITY without a key", { ...VMESS, tls: "reality" }, { kind: "realityKey" }],
    ["another version", { ...VMESS, v: "1" }, { kind: "vmessVersion", value: "1" }],
    ["no server", { ...VMESS, add: "" }, { kind: "address" }],
    ["a server with a port in it", { ...VMESS, add: "de.example.com:443" }, { kind: "address" }],
    ["no id", { ...VMESS, id: "" }, { kind: "noId" }],
    ["no port", { ...VMESS, port: "" }, { kind: "noPort" }],
    ["a bad port", { ...VMESS, port: "99999" }, { kind: "port", value: "99999" }],
  ])("refuses %s", (_, json, error) => {
    expect(refused(vmessLink(json))).toEqual(error);
  });

  it("refuses what isn't the base64 of a JSON object", () => {
    expect(refused("vmess://not*base64")).toEqual({ kind: "base64" });
    expect(refused("vmess://")).toEqual({ kind: "base64" });
    expect(refused("vmess://A")).toEqual({ kind: "base64" });
    expect(refused(`vmess://${utf8Base64("auto:uuid@host:443")}?remarks=x`)).toEqual({ kind: "vmessJson" });
    expect(refused(`vmess://${utf8Base64("[1,2]")}`)).toEqual({ kind: "vmessJson" });
    expect(refused(`vmess://${btoa("\xff\xfe{}")}`)).toEqual({ kind: "base64" });
  });
});

describe("trojan", () => {
  it("means TLS unless told otherwise, and decodes the password", () => {
    const got = ok("trojan://p%40ss%3Aw%23rd@example.com:443?sni=a.example.com&type=tcp&headerType=none#Trojan%20%F0%9F%87%A9%F0%9F%87%AA");
    expect(got.outbound).toEqual({
      type: "trojan",
      server: "example.com",
      server_port: 443,
      password: "p@ss:w#rd",
      tls_enabled: true,
      tls_server_name: "a.example.com",
    });
    expect(got.name).toBe("Trojan 🇩🇪");
  });

  it("takes peer as the server name, and security=none as no TLS", () => {
    expect(ok("trojan://pw@h.example:443?peer=p.example").outbound.tls_server_name).toBe("p.example");
    expect(ok("trojan://pw@h.example:443?security=none").outbound).toEqual({ type: "trojan", server: "h.example", server_port: 443, password: "pw" });
  });

  it("splits at the last @, so an unescaped one stays in the password", () => {
    expect(ok("trojan://p@ss@h.example:443").outbound.password).toBe("p@ss");
  });

  it.each([
    ["QUIC", "trojan://pw@h.example:443?type=quic", { kind: "transport", value: "quic" }],
    ["REALITY with a key that isn't one", "trojan://pw@h.example:443?security=reality&pbk=k", { kind: "realityKey" }],
    ["a flow", "trojan://pw@h.example:443?flow=xtls-rprx-direct", { kind: "flow", value: "xtls-rprx-direct" }],
    ["no password", "trojan://@h.example:443", { kind: "noPassword" }],
    ["a broken escape", "trojan://pw%zz@h.example:443", { kind: "encoding" }],
  ])("refuses %s", (_, link, error) => {
    expect(refused(link)).toEqual(error);
  });
});

describe("shadowsocks", () => {
  it("reads SIP002 with base64 user info", () => {
    const got = ok(`ss://${base64Url("aes-256-gcm:secret")}@198.51.100.4:8388#My%20SS`);
    expect(got.outbound).toEqual({ type: "shadowsocks", server: "198.51.100.4", server_port: 8388, method: "aes-256-gcm", password: "secret" });
    expect(got.name).toBe("My SS");
  });

  it("reads SIP002 with plain, percent-encoded user info, and an IPv6 server", () => {
    const got = ok("ss://2022-blake3-aes-256-gcm:YctPZ6U7xPPcU%2Bgp3u%2BXOCUkCtPgnC6bPWwBQjrZ%2BcQ%3D@[2001:db8::7]:8388/");
    expect(got.outbound).toEqual({
      type: "shadowsocks",
      server: "2001:db8::7",
      server_port: 8388,
      method: "2022-blake3-aes-256-gcm",
      password: "YctPZ6U7xPPcU+gp3u+XOCUkCtPgnC6bPWwBQjrZ+cQ=",
    });
  });

  it("reads the older form, base64 of the whole thing", () => {
    const got = ok(`ss://${utf8Base64("chacha20-ietf-poly1305:pass@old.example.com:8388")}#Old`);
    expect(got.outbound).toEqual({ type: "shadowsocks", server: "old.example.com", server_port: 8388, method: "chacha20-ietf-poly1305", password: "pass" });
    expect(got.name).toBe("Old");
  });

  it("keeps a colon in the password, and maps Xray's names for the chacha ciphers", () => {
    expect(ok(`ss://${utf8Base64("aes-128-gcm:a:b")}@h.example:1`).outbound.password).toBe("a:b");
    expect(ok(`ss://${utf8Base64("CHACHA20-POLY1305:pw")}@h.example:1`).outbound.method).toBe("chacha20-ietf-poly1305");
    expect(ok(`ss://${utf8Base64("xchacha20-poly1305:pw")}@h.example:1`).outbound.method).toBe("xchacha20-ietf-poly1305");
  });

  it("names the plugin it refuses, without its options", () => {
    expect(refused(`ss://${base64Url("aes-256-gcm:pw")}@h.example:8388/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dsecret.example`)).toEqual({
      kind: "plugin",
      value: "obfs-local",
    });
  });

  it.each([
    ["an unknown method", `ss://${utf8Base64("rc4-md5:pw")}@h.example:8388`, { kind: "method", value: "rc4-md5" }],
    ["no colon in the user info", `ss://${utf8Base64("aes-256-gcm")}@h.example:8388`, { kind: "ssFormat" }],
    ["an empty method", `ss://${utf8Base64(":pw")}@h.example:8388`, { kind: "ssFormat" }],
    ["no password", `ss://${utf8Base64("aes-256-gcm:")}@h.example:8388`, { kind: "noPassword" }],
    ["bad base64", "ss://!!!@h.example:8388", { kind: "base64" }],
    ["the older form without @", `ss://${utf8Base64("aes-256-gcm:pw")}`, { kind: "ssFormat" }],
    ["no port", `ss://${utf8Base64("aes-256-gcm:pw")}@h.example`, { kind: "noPort" }],
  ])("refuses %s", (_, link, error) => {
    expect(refused(link)).toEqual(error);
  });
});

describe("hysteria2", () => {
  it("takes the auth, the server name and insecure, with TLS always on", () => {
    const got = ok("hysteria2://pass%23word@example.com:8443/?sni=sni.example.com&insecure=1#HY2");
    expect(got.outbound).toEqual({
      type: "hysteria2",
      server: "example.com",
      server_port: 8443,
      password: "pass#word",
      tls_server_name: "sni.example.com",
      tls_insecure: true,
    });
    expect(got.name).toBe("HY2");
  });

  it("takes hy2:// too, with 443 when the port is left out, and the whole user:pass as auth", () => {
    expect(ok("hy2://user:pass@[2001:db8::2]/?sni=s.example").outbound).toEqual({
      type: "hysteria2",
      server: "2001:db8::2",
      server_port: 443,
      password: "user:pass",
      tls_server_name: "s.example",
    });
    expect(ok("hy2://pw@h.example").outbound.server_port).toBe(443);
  });

  it("lets obfs=none through, and an obfs password without obfs", () => {
    const got = ok("hy2://pw@h.example:443?obfs=none&obfs-password=x&security=tls");
    expect(got.outbound).toEqual({ type: "hysteria2", server: "h.example", server_port: 443, password: "pw" });
    expect(got.ignored).toEqual([]);
  });

  it("takes ALPN, and names the bandwidth hints and fingerprint it leaves out", () => {
    const got = ok("hy2://pw@h.example:443?alpn=h3&upmbps=100&downmbps=500&fp=chrome");
    expect(got.outbound.tls_alpn).toEqual(["h3"]);
    expect(got.ignored).toEqual(["upmbps", "downmbps", "fp"]);
  });

  it.each([
    ["salamander", "hy2://pw@h.example:443?obfs=salamander&obfs-password=x", { kind: "obfs", value: "salamander" }],
    ["a pinned certificate", "hy2://pw@h.example:443?pinSHA256=AB%3ACD", { kind: "pin" }],
    ["several ports", "hysteria2://pw@h.example:443,5000-6000/?sni=a", { kind: "portHopping", value: "443,5000-6000" }],
    ["a port range", "hy2://pw@[2001:db8::2]:20000-30000", { kind: "portHopping", value: "20000-30000" }],
    ["mport", "hy2://pw@h.example:443?mport=20000-30000", { kind: "portHopping", value: "20000-30000" }],
    ["no auth", "hy2://h.example:443", { kind: "noPassword" }],
    ["a bad port", "hy2://pw@h.example:abc", { kind: "port", value: "abc" }],
  ])("refuses %s", (_, link, error) => {
    expect(refused(link)).toEqual(error);
  });
});

describe("what isn't a link it can import", () => {
  it.each([
    ["nothing", "", { kind: "empty" }],
    ["only spaces", "  \n ", { kind: "empty" }],
    ["text", "hello there", { kind: "notLink" }],
    ["a scheme without //", "vless:abc", { kind: "notLink" }],
    ["a web address", "https://example.com/sub", { kind: "scheme", value: "https" }],
    ["tuic", "tuic://uuid:pw@h.example:443", { kind: "scheme", value: "tuic" }],
    ["hysteria v1", "hysteria://h.example:443?auth=x", { kind: "scheme", value: "hysteria" }],
    ["socks", "socks://dXNlcjpwYXNz@h.example:1080", { kind: "scheme", value: "socks" }],
  ])("refuses %s", (_, link, error) => {
    expect(refused(link)).toEqual(error);
  });

  it("ignores the spaces and line breaks a paste brings along", () => {
    expect(ok(`  vless://${UUID}@h.example:443\n`).outbound.server).toBe("h.example");
  });

  it("never puts a secret in the reason", () => {
    const secret = "s3cr3t-value";
    for (const link of [
      `trojan://${secret}@h.example:443?security=reality`,
      `ss://${utf8Base64(`aes-256-gcm:${secret}`)}@h.example:8388/?plugin=v2ray-plugin%3Bpath%3D%2F${secret}`,
      `hy2://${secret}@h.example:443?obfs=salamander&obfs-password=${secret}`,
      `vless://${secret}@h.example:443?type=xhttp`,
      `vless://${UUID}@h.example:443?security=reality&pbk=${PBK}&sid=${secret}&sni=s.example`,
      `vless://${UUID}@h.example:443?security=reality&pbk=${secret}&sni=s.example`,
      vmessLink({ ...VMESS, id: secret, net: "kcp" }),
    ]) {
      expect(JSON.stringify(refused(link))).not.toContain(secret);
    }
  });
});

describe("isIPv6", () => {
  it.each(["::", "::1", "2001:db8::1", "1:2:3:4:5:6:7:8", "fe80::1:2", "::ffff:192.0.2.1", "64:ff9b::198.51.100.1"])("accepts %s", (s) => {
    expect(isIPv6(s)).toBe(true);
  });
  it.each(["", "1.2.3.4", "example.com", ":1", "1::2::3", "1:2:3:4:5:6:7:8:9", "1:2:3:4:5:6:7", "12345::1", "::1.2.3.256", "g::1", "fe80::1%eth0"])(
    "refuses %s",
    (s) => {
      expect(isIPv6(s)).toBe(false);
    }
  );
});

// The JSON keys of outboundDTO, read from the Go source: what the server stores and validates.
// (Resolved without the URL class: under jsdom that is jsdom's, which node:fs doesn't take.)
const goOutboundKeys = (() => {
  const source = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "../../../../internal/httpapi/coreconfig.go"), "utf8");
  const body = /type outboundDTO struct \{([\s\S]*?)\n\}/.exec(source)?.[1] ?? "";
  return new Set([...body.matchAll(/json:"([a-z_]+)/g)].map((m) => m[1]));
})();

describe("every outbound it makes", () => {
  const links = [
    `vless://${UUID}@example.com:443?flow=xtls-rprx-vision&security=tls&sni=s.example&allowInsecure=1`,
    `vless://${UUID}@[2001:db8::1]:80`,
    vmessLink(VMESS),
    vmessLink({ ...VMESS, tls: "none", scy: "chacha20-poly1305" }),
    "trojan://pw@h.example:443?sni=s.example",
    "trojan://pw@h.example:80?security=none",
    `ss://${base64Url("2022-blake3-aes-128-gcm:a2V5a2V5a2V5a2V5a2V5aw==")}@h.example:8388`,
    "hy2://pw@h.example:443?sni=s.example&insecure=1&alpn=h3",
    `vless://${UUID}@203.0.113.9:443?flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=${PBK}&sid=0123abcd&spx=%2F`,
    `vless://${UUID}@203.0.113.9:443?security=reality&sni=www.example.com&pbk=${PBK}&type=grpc&serviceName=svc`,
    `vless://${UUID}@cdn.example.com:443?security=tls&sni=cdn.example.com&fp=firefox&alpn=http%2F1.1&type=ws&path=%2Fray&host=cdn.example.com`,
    `vless://${UUID}@h.example:80?type=httpupgrade&path=%2Fup&host=h.example`,
    `vless://${UUID}@h.example:443?security=tls&type=h2&path=%2Fh2&host=a.example%2Cb.example`,
    `trojan://pw@h.example:443?sni=i.example&ech=${encodeURIComponent(ECH)}`,
    vmessLink({ ...VMESS, net: "ws", path: "/ws", host: "cdn.example.com" }),
    vmessLink({ ...VMESS, net: "grpc", path: "svc" }),
  ];

  it("reads the server's own field list", () => {
    expect(goOutboundKeys).toContain("tls_server_name");
    expect(goOutboundKeys.size).toBeGreaterThan(20);
  });

  it.each(links)("uses only the server's fields, passes its checks and survives the form: %s", (link) => {
    const { outbound } = ok(link);
    expect(outbound).not.toHaveProperty("tag");
    for (const key of Object.keys(outbound)) expect(goOutboundKeys, key).toContain(key);
    const item = { tag: "imported", ...outbound };
    expect(validateCoreConfigDoc({ outbounds: [item] })).toEqual([]);
    expect(normalizeOutbound(item, OUTBOUND_TYPES_PLAIN)).toEqual(item);
  });
});

describe("messages", () => {
  it("has every reason in every language", () => {
    const locales: Record<string, Record<string, string>> = { en, fa, ru, zh };
    for (const [lang, dict] of Object.entries(locales)) {
      for (const kind of LINK_ERROR_KINDS) expect(dict[`rapido.coreForm.link.error.${kind}`], `${lang} ${kind}`).toBeTruthy();
      for (const kind of LINK_NOTE_KINDS) expect(dict[`rapido.coreForm.link.note.${kind}`], `${lang} ${kind}`).toBeTruthy();
    }
  });
});
