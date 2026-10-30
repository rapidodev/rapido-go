import { describe, expect, it } from "vitest";
import { OUTBOUND_GROUP_TYPES, OUTBOUND_TYPES } from "types/CoreConfig";
import { INBOUND_PROTOCOLS } from "types/Inbound";
import { generatePassword, generateRealityKeyPair, generateShortId, generateSnellPsk, generateSs2022Key } from "../coreConfigGenerate";
import {
  OUTBOUND_TYPES_PLAIN,
  dnsFields,
  inboundDefaults,
  inboundFields,
  normalizeDns,
  normalizeInbound,
  normalizeOutbound,
  normalizeRule,
  outboundDefaults,
  outboundFields,
  ruleFields,
  splitList,
} from "../coreConfigFields";

const keys = (fields: { key: string }[]) => fields.map((f) => f.key);

describe("outboundFields", () => {
  it("offers a form for every type the server accepts", () => {
    for (const type of OUTBOUND_TYPES) {
      const fields = outboundFields({ tag: "x", type });
      expect(keys(fields).slice(0, 2), type).toEqual(["tag", "type"]);
    }
  });

  it("keeps balancers out of the plain outbound picker", () => {
    expect(OUTBOUND_TYPES_PLAIN).not.toContain("urltest");
    expect(OUTBOUND_TYPES_PLAIN).not.toContain("selector");
    expect(OUTBOUND_TYPES_PLAIN.length + OUTBOUND_GROUP_TYPES.length).toBe(OUTBOUND_TYPES.length);
  });

  it("shows only what a type uses", () => {
    expect(keys(outboundFields({ type: "direct" }))).toEqual(["tag", "type", "bind_interface"]);
    expect(keys(outboundFields({ type: "block" }))).toEqual(["tag", "type"]);
    expect(keys(outboundFields({ type: "socks" }))).toEqual(["tag", "type", "server", "server_port", "username", "password", "bind_interface"]);
    expect(keys(outboundFields({ type: "tor" }))).toEqual(["tag", "type", "data_directory", "bind_interface"]);
    expect(keys(outboundFields({ type: "urltest" }))).toEqual(["tag", "type", "outbounds", "url", "interval", "tolerance"]);
    expect(keys(outboundFields({ type: "selector" }))).toEqual(["tag", "type", "outbounds", "default"]);
  });

  it("reveals the TLS details and the fallback only once they apply", () => {
    expect(keys(outboundFields({ type: "trojan" }))).not.toContain("tls_server_name");
    expect(keys(outboundFields({ type: "trojan", tls_enabled: true }))).toContain("tls_server_name");
    expect(keys(outboundFields({ type: "hysteria2" }))).toContain("tls_server_name");
    expect(keys(outboundFields({ type: "hysteria2" }))).not.toContain("tls_enabled");
    expect(keys(outboundFields({ type: "direct" }))).not.toContain("direct_fallback");
    expect(keys(outboundFields({ type: "direct", bind_interface: "wg0" }))).toContain("direct_fallback");
    expect(keys(outboundFields({ type: "snell" }))).not.toContain("obfs_host");
    expect(keys(outboundFields({ type: "snell", obfs_mode: "tls" }))).toContain("obfs_host");
  });

  it("marks the shadowtls password required except on version 1", () => {
    const required = (version: number) => outboundFields({ type: "shadowtls", version }).find((f) => f.key === "password")?.required;
    expect(required(1)).toBe(false);
    expect(required(3)).toBe(true);
  });
});

describe("ruleFields", () => {
  it("offers inbound_port only with an inbound selected, and every matcher the server knows", () => {
    expect(keys(ruleFields({}))).not.toContain("inbound_port");
    const all = keys(ruleFields({ inbound: ["main"] }));
    for (const key of ["outbound_tag", "inbound", "inbound_port", "auth_user", "source_ip_cidr", "source_port", "source_port_range", "source_ip_is_private", "domain", "domain_suffix", "domain_keyword", "domain_regex", "ip_cidr", "port", "port_range", "ip_is_private", "network", "protocol"]) {
      expect(all, key).toContain(key);
    }
  });
});

describe("dnsFields", () => {
  it("follows the type", () => {
    expect(keys(dnsFields({ type: "local" }))).toEqual(["tag", "type"]);
    expect(keys(dnsFields({ type: "udp" }))).toEqual(["tag", "type", "address", "port"]);
    expect(keys(dnsFields({ type: "https" }))).toEqual(["tag", "type", "address", "port", "path"]);
  });
});

describe("inboundFields", () => {
  it("offers a form for every protocol the server accepts", () => {
    for (const protocol of INBOUND_PROTOCOLS) expect(keys(inboundFields({ protocol }, false)).slice(0, 2)).toEqual(["tag", "protocol"]);
  });

  it("follows the security choice", () => {
    expect(keys(inboundFields({ protocol: "vless", security: "none" }, false))).toEqual(["tag", "protocol", "network", "header_type", "security"]);
    expect(keys(inboundFields({ protocol: "vless", security: "reality" }, false))).toContain("reality_private_key");
    expect(keys(inboundFields({ protocol: "vless", security: "tls" }, false))).toContain("tls_certificate");
  });

  it("treats TLS as a given on the protocols that require it, and as absent on those that can't use it", () => {
    const hy2 = keys(inboundFields({ protocol: "hysteria2" }, false));
    expect(hy2).not.toContain("security");
    expect(hy2).toContain("tls_certificate");
    const snell = keys(inboundFields({ protocol: "snell" }, false));
    expect(snell).not.toContain("security");
    expect(snell).not.toContain("tls_certificate");
  });

  it("locks the tag of an inbound the server already has", () => {
    expect(inboundFields({ protocol: "vless" }, true)[0].disabled).toBe(true);
    expect(inboundFields({ protocol: "vless" }, false)[0].disabled).toBe(false);
  });
});

describe("normalizing on save", () => {
  it("drops what the outbound's type doesn't use, and keeps a key the form doesn't know", () => {
    expect(
      normalizeOutbound({ tag: "x", type: "direct", server: "s", server_port: 1, password: "p", future_field: 1 })
    ).toEqual({ tag: "x", type: "direct", future_field: 1 });
  });

  it("trims the interface, and clearing it takes direct_fallback along", () => {
    expect(normalizeOutbound({ tag: "x", type: "direct", bind_interface: " wg0 " })).toEqual({ tag: "x", type: "direct", bind_interface: "wg0" });
    expect(normalizeOutbound({ tag: "x", type: "direct", bind_interface: "  ", direct_fallback: true })).toEqual({ tag: "x", type: "direct" });
  });

  it("drops inbound_port together with the last inbound", () => {
    expect(normalizeRule({ inbound_port: [1], outbound_tag: "a" })).toEqual({ outbound_tag: "a" });
    expect(normalizeRule({ inbound: ["m"], inbound_port: [1], outbound_tag: "a" })).toEqual({ inbound: ["m"], inbound_port: [1], outbound_tag: "a" });
  });

  it("drops DNS fields the type doesn't use", () => {
    expect(normalizeDns({ tag: "d", type: "local", address: "1.1.1.1", port: 53, path: "/x" })).toEqual({ tag: "d", type: "local" });
    expect(normalizeDns({ tag: "d", type: "udp", address: "1.1.1.1", path: "/x" })).toEqual({ tag: "d", type: "udp", address: "1.1.1.1" });
  });

  it("settles the security a protocol fixes, whatever the item said", () => {
    expect(normalizeInbound({ tag: "a", protocol: "hysteria2", security: "none" }, false).security).toBe("tls");
    expect(normalizeInbound({ tag: "a", protocol: "tuic" }, false).security).toBe("tls");
    expect(normalizeInbound({ tag: "a", protocol: "snell", security: "tls", snell_psk: "k" }, false).security).toBe("none");
    expect(normalizeInbound({ tag: "a", protocol: "vless", security: "reality" }, false).security).toBe("reality");
  });

  it("keeps TLS material on a protocol that requires it", () => {
    const item = normalizeInbound({ tag: "a", protocol: "hysteria2", tls_certificate: "C", tls_key: "K" }, false);
    expect(item.tls_certificate).toBe("C");
    expect(item.tls_key).toBe("K");
  });

  it("drops settings left over from another protocol", () => {
    const item = normalizeInbound({ tag: "a", protocol: "vless", security: "none", snell_psk: "k", up_mbps: 1, reality_private_key: "r" }, false);
    expect(item).toEqual({ tag: "a", protocol: "vless", security: "none" });
  });

  it("removes the transport from a protocol that has none", () => {
    expect(normalizeInbound({ tag: "a", protocol: "tuic", network: "tcp", header_type: "" }, false)).toEqual({ tag: "a", protocol: "tuic", security: "tls" });
  });
});

describe("defaults", () => {
  it("fill in only what is missing", () => {
    expect(outboundDefaults({ type: "vmess" }).security).toBe("auto");
    expect(outboundDefaults({ type: "vmess", security: "none" }).security).toBe("none");
    expect(outboundDefaults({ type: "shadowtls" }).version).toBe(3);
    expect(outboundDefaults({ type: "direct" })).toEqual({ type: "direct" });
    expect(inboundDefaults({ protocol: "vless" }).network).toBe("tcp");
    expect(inboundDefaults({ protocol: "shadowtls" }).shadowtls_inner_method).toBe("2022-blake3-aes-128-gcm");
    expect(inboundDefaults({ protocol: "tuic" })).toEqual({ protocol: "tuic" });
  });
});

describe("splitList", () => {
  it("splits on the separators a person actually types", () => {
    expect(splitList("a, b\nc;d،e  f")).toEqual(["a", "b", "c", "d", "e", "f"]);
    expect(splitList("  ,, ")).toEqual([]);
  });
});

describe("generators", () => {
  it("make values of the right shape, different each time", () => {
    expect(generatePassword()).toMatch(/^[A-Za-z0-9_-]{24}$/);
    expect(generatePassword()).not.toBe(generatePassword());
    expect(generateSnellPsk()).toMatch(/^[0-9a-f]{32}$/);
    expect(generateShortId()).toMatch(/^[0-9a-f]{16}$/);
    expect(atob(generateSs2022Key(16))).toHaveLength(16);
    expect(atob(generateSs2022Key(32))).toHaveLength(32);
  });

  it("makes a Reality key pair sing-box can read, or says it can't", async () => {
    const pair = await generateRealityKeyPair();
    if (pair === null) return; // no X25519 in this runtime; the form reports that to the admin
    expect(pair.privateKey).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(pair.publicKey).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(pair.privateKey).not.toBe(pair.publicKey);
  });
});
