import { describe, expect, it } from "vitest";
import { topLevelKeyLines } from "../coreConfigRules";

describe("topLevelKeyLines", () => {
  it("finds each top-level key's line in a formatted document", () => {
    const text = JSON.stringify({ log_level: "warn", outbounds: [{ tag: "a" }], routing_rules: [] }, null, 2);
    expect(topLevelKeyLines(text)).toEqual({ log_level: 2, outbounds: 3, routing_rules: 8 });
  });

  it("ignores the same key nested deeper, and strings that only look like keys", () => {
    const text = ['{', '  "outbounds": [', '    { "inbounds": "x", "tag": "routing_rules" }', '  ],', '  "inbounds": []', '}'].join("\n");
    expect(topLevelKeyLines(text)).toEqual({ outbounds: 2, inbounds: 5 });
  });

  it("is not fooled by brackets or escaped quotes inside a string", () => {
    const text = ['{', '  "a": "}\\"{[",', '  "b": 1', '}'].join("\n");
    expect(topLevelKeyLines(text)).toEqual({ a: 2, b: 3 });
  });

  it("keeps working on invalid JSON, where an open string only costs its own line", () => {
    const text = ['{', '  "log_level": "warn,', '  "outbounds": [', '  ],', '  "inbounds": []', '}'].join("\n");
    expect(topLevelKeyLines(text)).toEqual({ log_level: 2, outbounds: 3, inbounds: 5 });
  });

  it("copes with a document that is not an object", () => {
    expect(topLevelKeyLines("")).toEqual({});
    expect(topLevelKeyLines("[]")).toEqual({});
  });
});
