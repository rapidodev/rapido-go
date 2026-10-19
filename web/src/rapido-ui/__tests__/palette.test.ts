import { describe, expect, it } from "vitest";
import {
  DEFAULT_PALETTE,
  contrastIssues,
  contrastRatio,
  hexToRgb,
  isHex,
  isLight,
  luminance,
  mixHex,
  paletteVars,
  readableInk,
  rgbToHex,
} from "../palette";

describe("colour maths", () => {
  it("recognises only #rrggbb", () => {
    expect(isHex("#22d3ee")).toBe(true);
    expect(isHex("#22D3EE")).toBe(true);
    expect(isHex("#fff")).toBe(false);
    expect(isHex("22d3ee")).toBe(false);
    expect(isHex(12)).toBe(false);
  });

  it("converts between hex and rgb and mixes", () => {
    expect(hexToRgb("#0a1116")).toEqual([10, 17, 22]);
    expect(rgbToHex([10, 17, 22])).toBe("#0a1116");
    expect(mixHex("#000000", "#ffffff", 0.5)).toBe("#808080");
    expect(mixHex("#123456", "#ffffff", 0)).toBe("#123456");
  });

  it("computes WCAG relative luminance and contrast ratio", () => {
    expect(luminance("#000000")).toBe(0);
    expect(luminance("#ffffff")).toBeCloseTo(1, 5);
    expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 3);
    expect(contrastRatio("#ffffff", "#000000")).toBeCloseTo(21, 3);
    expect(contrastRatio("#777777", "#777777")).toBe(1);
    // the well-known threshold pair: #767676 on white is 4.54:1
    expect(contrastRatio("#767676", "#ffffff")).toBeCloseTo(4.54, 1);
  });

  it("picks dark or white ink for a fill, and says which colours are light", () => {
    expect(readableInk("#22d3ee")).toBe("#04141a");
    expect(readableInk("#0a1116")).toBe("#ffffff");
    expect(isLight("#eef2f7")).toBe(true);
    expect(isLight("#0a1116")).toBe(false);
  });
});

describe("palette to CSS variables", () => {
  it("is empty for an empty palette, so nothing overrides the preset", () => {
    expect(paletteVars({})).toEqual({});
  });

  it("maps the brand slots onto the accent, secondary and tertiary tokens, with readable ink on the primary", () => {
    const v = paletteVars({ primary: "#ff0000", secondary: "#00ff00", tertiary: "#0000ff" });
    expect(v["--rp-accent"]).toBe("255 0 0");
    expect(v["--rp-violet"]).toBe("0 255 0");
    expect(v["--rp-tertiary"]).toBe("0 0 255");
    expect(v["--rp-teal"]).toBe("0 0 255");
    expect(v["--rp-on-accent"]).toBe("4 20 26");
  });

  it("derives a light and a dark shade for each status colour", () => {
    const v = paletteVars({ good: "#36eab0" });
    expect(v["--rp-good"]).toBe("54 234 176");
    expect(v["--rp-good-hi"]).not.toBe(v["--rp-good"]);
    expect(v["--rp-good-lo"]).not.toBe(v["--rp-good"]);
    expect(Object.keys(paletteVars({ warn: "#ffbe4a", bad: "#ff6870" })).sort()).toEqual(
      ["--rp-bad", "--rp-bad-hi", "--rp-bad-lo", "--rp-warn", "--rp-warn-hi", "--rp-warn-lo"].sort()
    );
  });

  it("derives every surface and the text colours from a background and surface, and the scene sky", () => {
    const dark = paletteVars({ background: "#0a1116", surface: "#111c22" });
    expect(dark["--rp-bg"]).toBe("10 17 22");
    expect(dark["--rp-surface"]).toBe("17 28 34");
    for (const k of ["--rp-raised", "--rp-border", "--rp-inset", "--rp-text", "--rp-muted", "--w-sky-1", "--w-sky-2", "--w-floor-1"]) expect(dark[k]).toBeTruthy();
    expect(dark["--rp-text"]).toBe("231 241 244"); // light text on a dark page
    const light = paletteVars({ background: "#eef2f7", surface: "#ffffff" });
    expect(light["--rp-text"]).toBe("15 23 42"); // dark text on a light page
    // a light page makes the status "hi" shade the darker one, so text in it stays readable
    const lightGood = paletteVars({ background: "#eef2f7", good: "#10b981" });
    const darkGood = paletteVars({ background: "#0a1116", good: "#10b981" });
    expect(lightGood["--rp-good-hi"]).not.toBe(darkGood["--rp-good-hi"]);
  });

  it("makes a surface up when only the background is set", () => {
    const v = paletteVars({ background: "#101820" });
    expect(v["--rp-surface"]).toBeTruthy();
    expect(v["--rp-bg"]).toBe("16 24 32");
  });

  it("ignores anything that is not a valid colour", () => {
    expect(paletteVars({ primary: "red", good: "#12" })).toEqual({});
  });
});

describe("contrast warnings", () => {
  it("has none for the built-in palette, nor for an empty one", () => {
    expect(contrastIssues({})).toEqual([]);
    expect(contrastIssues(DEFAULT_PALETTE)).toEqual([]);
  });

  it("warns when the primary colour almost matches the surface it is drawn on", () => {
    const issues = contrastIssues({ primary: "#13222a" });
    const hit = issues.find((i) => i.pair[0] === "primary");
    expect(hit).toBeTruthy();
    expect(hit?.pair).toEqual(["primary", "surface"]);
    expect(hit?.min).toBe(3);
    expect(hit?.ratio).toBeLessThan(3);
  });

  it("warns about a status colour that disappears into a light surface", () => {
    const issues = contrastIssues({ background: "#ffffff", surface: "#ffffff", warn: "#fff3c4" });
    expect(issues.map((i) => i.pair.join(">"))).toContain("warn>surface");
  });

  it("warns when the surface and the page are the same colour, so nothing is layered", () => {
    const issues = contrastIssues({ background: "#202830", surface: "#202830" });
    expect(issues.map((i) => i.pair.join(">"))).toContain("surface>background");
  });

  it("warns about text on a mid-tone background, where neither light nor dark text reads well", () => {
    const issues = contrastIssues({ background: "#7a7a7a", surface: "#7a7a7a" });
    expect(issues.some((i) => i.pair[0] === "text" || i.pair[0] === "muted")).toBe(true);
  });
});
