import { describe, expect, it } from "vitest";
import cases from "./core-config-cases.json";
import {
  inboundReferences,
  isGroupOutbound,
  isIpOrCidr,
  isLeafOutboundType,
  isPortRange,
  isPositiveDuration,
  moveItem,
  otherMatcherSummary,
  otherTags,
  outboundReferences,
  outboundSupportsDirectFallback,
  outboundTargets,
  renameInboundReferences,
  renameOutboundReferences,
  uniqueTag,
  validateCoreConfigDoc,
} from "../coreConfigRules";

describe("isLeafOutboundType", () => {
  it("is false for selector, urltest and block, true for dialing types", () => {
    for (const type of ["selector", "urltest", "block"]) expect(isLeafOutboundType(type)).toBe(false);
    for (const type of ["direct", "socks", "vless", "shadowsocks", "hysteria2"]) {
      expect(isLeafOutboundType(type)).toBe(true);
    }
  });

  it("is false for a missing or non-string type", () => {
    expect(isLeafOutboundType(undefined)).toBe(false);
    expect(isLeafOutboundType("")).toBe(false);
    expect(isLeafOutboundType(3)).toBe(false);
  });
});

describe("isGroupOutbound", () => {
  it("is true only for the balancer types", () => {
    expect(isGroupOutbound({ type: "urltest" })).toBe(true);
    expect(isGroupOutbound({ type: "selector" })).toBe(true);
    expect(isGroupOutbound({ type: "direct" })).toBe(false);
    expect(isGroupOutbound(null)).toBe(false);
  });
});

describe("outboundSupportsDirectFallback", () => {
  it("needs both a leaf type and a non-blank bind interface", () => {
    expect(outboundSupportsDirectFallback({ type: "direct", bind_interface: "wg0" })).toBe(true);
    expect(outboundSupportsDirectFallback({ type: "direct" })).toBe(false);
    expect(outboundSupportsDirectFallback({ type: "direct", bind_interface: "  " })).toBe(false);
    expect(outboundSupportsDirectFallback({ type: "selector", bind_interface: "wg0" })).toBe(false);
  });
});

describe("otherMatcherSummary", () => {
  it("lists only the matchers a rule uses, ignoring inbound, inbound_port and outbound_tag", () => {
    expect(
      otherMatcherSummary({
        inbound: ["main"],
        inbound_port: [20000],
        domain_suffix: [".ir"],
        network: ["tcp", "udp"],
        outbound_tag: "wg",
      })
    ).toEqual(["domain_suffix: .ir", "network: tcp, udp"]);
  });

  it("truncates a long value list with a hidden count", () => {
    expect(otherMatcherSummary({ ip_cidr: ["a", "b", "c", "d", "e"], outbound_tag: "x" })).toEqual([
      "ip_cidr: a, b, c (+2)",
    ]);
  });

  it("includes the flags and the source matchers, and is empty for a rule with no other matchers", () => {
    expect(otherMatcherSummary({ ip_is_private: true, outbound_tag: "x" })).toEqual(["ip_is_private"]);
    expect(otherMatcherSummary({ source_ip_is_private: true, auth_user: ["ali"], source_port: [53], outbound_tag: "x" })).toEqual([
      "auth_user: ali",
      "source_port: 53",
      "source_ip_is_private",
    ]);
    expect(otherMatcherSummary({ outbound_tag: "x" })).toEqual([]);
  });
});

describe("value checks", () => {
  it("isIpOrCidr takes what sing-box takes", () => {
    for (const ok of ["1.1.1.1", "10.0.0.0/8", "0.0.0.0/0", "fc00::/7", "2001:db8::1", "::1", "::/0"]) expect(isIpOrCidr(ok), ok).toBe(true);
    for (const bad of ["", "geoip:ir", "10.0.0.0/33", "fc00::/129", "1.2.3", "1.2.3.4.5", "256.1.1.1", "010.0.0.1", "1.1.1.1/", "1.1.1.1/8/8", "example.com", "[::1]"]) {
      expect(isIpOrCidr(bad), bad).toBe(false);
    }
  });

  it("isPortRange takes start:end with one side optional", () => {
    for (const ok of ["1000:2000", ":2000", "1000:", "0:65535", "5:5"]) expect(isPortRange(ok), ok).toBe(true);
    for (const bad of ["", ":", "1000", "2000:1000", "1:70000", "70000:", "a:b", "1-2"]) expect(isPortRange(bad), bad).toBe(false);
  });

  it("isPositiveDuration takes a Go duration above zero", () => {
    for (const ok of ["3m", "1h30m", "90s", "1.5h", "100ms"]) expect(isPositiveDuration(ok), ok).toBe(true);
    for (const bad of ["", "0s", "0", "3", "soon", "-1m", "3 m"]) expect(isPositiveDuration(bad), bad).toBe(false);
  });
});

describe("validateCoreConfigDoc", () => {
  // The same file drives TestCoreConfigValidationMatchesDashboard on the server, so
  // the form can neither refuse what Apply would take nor pass what it would reject.
  it.each(cases.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    const issues = validateCoreConfigDoc(c.doc);
    if (c.valid) expect(issues).toEqual([]);
    else expect(issues.length).toBeGreaterThan(0);
  });

  it("does not throw on non-objects or wrongly shaped sections", () => {
    expect(validateCoreConfigDoc(null)).toEqual([]);
    expect(validateCoreConfigDoc([])).toEqual([]);
    expect(validateCoreConfigDoc({ outbounds: "x" })).toEqual([]);
    expect(validateCoreConfigDoc({ routing_rules: [1, null, "y"] }).map((i) => i.kind)).toEqual(["notAnObject", "notAnObject", "notAnObject"]);
  });

  it("reports an inbound_port problem with the rule position and its outbound", () => {
    const issues = validateCoreConfigDoc({
      inbounds: [{ tag: "main", protocol: "vless" }],
      outbounds: [
        { tag: "ok", type: "direct" },
        { tag: "wg-de", type: "direct" },
        { tag: "wg-nl", type: "direct" },
      ],
      routing_rules: [
        { inbound: ["main"], inbound_port: [20000], outbound_tag: "ok" },
        { inbound: ["main"], inbound_port: [20000, 20000], outbound_tag: "wg-de" },
        { inbound_port: [20001], outbound_tag: "wg-nl" },
      ],
    });
    expect(issues).toEqual([
      { scope: "rule", index: 1, ref: "wg-de", kind: "inboundPortDuplicate", field: "inbound_port", value: "20000" },
      { scope: "rule", index: 2, ref: "wg-nl", kind: "inboundPortNeedsInbound", field: "inbound_port", value: undefined },
    ]);
  });

  it("maps each port problem to its own kind", () => {
    const kind = (inbound_port: unknown) =>
      validateCoreConfigDoc({
        inbounds: [{ tag: "m", protocol: "vless" }],
        routing_rules: [{ inbound: ["m"], inbound_port, outbound_tag: "direct" }],
      })[0]?.kind;
    expect(kind("20000")).toBe("inboundPortNotList");
    expect(kind([1.5])).toBe("inboundPortNotInteger");
    expect(kind([0])).toBe("inboundPortOutOfRange");
    expect(kind([5, 5])).toBe("inboundPortDuplicate");
  });

  it("points each problem at the field that has it", () => {
    const issues = validateCoreConfigDoc({ outbounds: [{ tag: "", type: "vless", server_port: 70000 }] });
    expect(issues.map((i) => [i.kind, i.field])).toEqual([
      ["tagRequired", "tag"],
      ["serverPortInvalid", "server_port"],
      ["serverRequired", "server"],
      ["uuidRequired", "uuid"],
    ]);
  });

  it("accepts direct_fallback on a leaf outbound with an interface, and ignores false/null", () => {
    expect(
      validateCoreConfigDoc({
        outbounds: [
          { tag: "a", type: "direct", bind_interface: "wg0", direct_fallback: true },
          { tag: "b", type: "selector", outbounds: ["a"], direct_fallback: false },
          { tag: "c", type: "block", direct_fallback: null },
        ],
      })
    ).toEqual([]);
  });

  it("rejects a non-boolean direct_fallback", () => {
    expect(
      validateCoreConfigDoc({ outbounds: [{ tag: "a", type: "direct", bind_interface: "wg0", direct_fallback: "yes" }] }).map((i) => i.kind)
    ).toEqual(["directFallbackNotBoolean"]);
  });

  it("names a group in a loop only once", () => {
    const issues = validateCoreConfigDoc({
      outbounds: [
        { tag: "x", type: "selector", outbounds: ["y"] },
        { tag: "y", type: "selector", outbounds: ["x"] },
      ],
    });
    expect(issues.filter((i) => i.kind === "dependencyCycle")).toHaveLength(1);
  });

  it("does not hang on a long chain of groups", () => {
    const outbounds = Array.from({ length: 2000 }, (_, i) => ({ tag: `g${i}`, type: "selector", outbounds: [i + 1 < 2000 ? `g${i + 1}` : "direct"] }));
    expect(validateCoreConfigDoc({ outbounds })).toEqual([]);
  });
});

describe("document helpers", () => {
  const doc = {
    outbounds: [
      { tag: "a", type: "direct" },
      { tag: "b", type: "direct" },
      { tag: "g", type: "urltest", outbounds: ["a", "b"] },
      "junk",
    ],
    routing_rules: [
      { inbound: ["main"], outbound_tag: "a" },
      { outbound_tag: "g" },
      { inbound: ["main", "other"], outbound_tag: "a" },
    ],
    inbounds: [{ tag: "main" }, { tag: "" }],
  };

  it("otherTags leaves out the item being edited, and anything without a tag", () => {
    expect([...otherTags(doc, "inbounds", null)]).toEqual(["main"]);
    expect([...otherTags(doc, "outbounds", 0)]).toEqual(["b", "g"]);
    expect([...otherTags(null, "outbounds", null)]).toEqual([]);
  });

  it("outboundTargets adds the two built-ins", () => {
    expect(outboundTargets(doc)).toEqual(["direct", "block", "a", "b", "g"]);
  });

  it("uniqueTag keeps a free name and numbers a taken one, and never hands out a built-in", () => {
    expect(uniqueTag("x", new Set())).toBe("x");
    expect(uniqueTag("x", new Set(["x"]))).toBe("x-2");
    expect(uniqueTag("x", new Set(["x", "x-2"]))).toBe("x-3");
    expect(uniqueTag("direct", new Set())).toBe("direct-2");
  });

  it("moveItem moves without changing the original", () => {
    const list = [1, 2, 3, 4];
    expect(moveItem(list, 0, 2)).toEqual([2, 3, 1, 4]);
    expect(moveItem(list, 3, 0)).toEqual([4, 1, 2, 3]);
    expect(list).toEqual([1, 2, 3, 4]);
  });

  it("finds what refers to an outbound or an inbound", () => {
    expect(outboundReferences(doc, "a")).toEqual({ rules: [0, 2], groups: ["g"] });
    expect(outboundReferences(doc, "g")).toEqual({ rules: [1], groups: [] });
    expect(outboundReferences(null, "a")).toEqual({ rules: [], groups: [] });
    expect(inboundReferences(doc, "main")).toEqual([0, 2]);
    expect(inboundReferences(doc, "other")).toEqual([2]);
  });

  it("a renamed outbound takes its rules and balancers along", () => {
    const next = renameOutboundReferences(doc, "a", "z");
    expect(outboundReferences(next, "a")).toEqual({ rules: [], groups: [] });
    expect(outboundReferences(next, "z")).toEqual({ rules: [0, 2], groups: ["g"] });
    expect(outboundReferences(doc, "a").rules).toEqual([0, 2]);
  });

  it("a renamed inbound takes its rules along, leaving the others in them", () => {
    const next = renameInboundReferences(doc, "main", "new");
    expect(inboundReferences(next, "new")).toEqual([0, 2]);
    expect(inboundReferences(next, "other")).toEqual([2]);
  });
});
