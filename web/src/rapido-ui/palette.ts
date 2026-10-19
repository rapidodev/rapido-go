// A custom palette, as pure functions: colour maths (WCAG contrast), the CSS
// variables a palette turns into, and the checks the Theme page runs on one.
// The variables are the same tokens every dial, chart, pill, button, glow and
// the 3D scene already read (see tailwind.css), so a palette really changes
// the whole panel and nothing else needs to know about it.

export const PALETTE_KEYS = ["primary", "secondary", "tertiary", "good", "warn", "bad", "background", "surface"] as const;
export type PaletteKey = (typeof PALETTE_KEYS)[number];
export type Palette = Partial<Record<PaletteKey, string>>;

/** The built-in cockpit colours: what each slot is when nothing is customised. */
export const DEFAULT_PALETTE: Record<PaletteKey, string> = {
  primary: "#22d3ee",
  secondary: "#a78bfa",
  tertiary: "#36eab0",
  good: "#36eab0",
  warn: "#ffbe4a",
  bad: "#ff6870",
  background: "#0a1116",
  surface: "#111c22",
};

const HEX = /^#[0-9a-f]{6}$/i;
export const isHex = (s: unknown): s is string => typeof s === "string" && HEX.test(s);

export const hexToRgb = (hex: string): [number, number, number] => [
  parseInt(hex.slice(1, 3), 16),
  parseInt(hex.slice(3, 5), 16),
  parseInt(hex.slice(5, 7), 16),
];

const clamp8 = (n: number) => Math.min(255, Math.max(0, Math.round(n)));
export const rgbToHex = ([r, g, b]: [number, number, number]): string =>
  `#${[r, g, b].map((n) => clamp8(n).toString(16).padStart(2, "0")).join("")}`;

/** `t` of the way from a to b (0 = a, 1 = b), in sRGB. */
export const mixHex = (a: string, b: string, t: number): string => {
  const x = hexToRgb(a);
  const y = hexToRgb(b);
  return rgbToHex([x[0] + (y[0] - x[0]) * t, x[1] + (y[1] - x[1]) * t, x[2] + (y[2] - x[2]) * t]);
};

/** WCAG 2 relative luminance. */
export const luminance = (hex: string): number => {
  const [r, g, b] = hexToRgb(hex).map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};

/** WCAG 2 contrast ratio between two colours, 1..21. */
export const contrastRatio = (a: string, b: string): number => {
  const la = luminance(a);
  const lb = luminance(b);
  const [hi, lo] = la >= lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
};

export const isLight = (hex: string): boolean => luminance(hex) > 0.4;

/** Dark ink or white, whichever reads better on `bg`. */
export const readableInk = (bg: string): string =>
  contrastRatio(bg, "#04141a") >= contrastRatio(bg, "#ffffff") ? "#04141a" : "#ffffff";

const triple = (hex: string) => hexToRgb(hex).join(" ");

/** Every CSS variable this module may set; used to clear them when a palette is removed. */
export const PALETTE_VARS = [
  "--rp-accent",
  "--rp-on-accent",
  "--rp-violet",
  "--rp-tertiary",
  "--rp-teal",
  "--rp-good",
  "--rp-good-hi",
  "--rp-good-lo",
  "--rp-warn",
  "--rp-warn-hi",
  "--rp-warn-lo",
  "--rp-bad",
  "--rp-bad-hi",
  "--rp-bad-lo",
  "--rp-bg",
  "--rp-surface",
  "--rp-raised",
  "--rp-border",
  "--rp-inset",
  "--rp-text",
  "--rp-muted",
  "--w-sky-1",
  "--w-sky-2",
  "--w-floor-1",
] as const;

/** The variables for the slots a palette sets; unset slots add nothing, so the preset's own colour stays. */
export const paletteVars = (p: Palette): Record<string, string> => {
  const out: Record<string, string> = {};
  const set = (k: string, hex: string) => (out[k] = triple(hex));
  const bg = isHex(p.background) ? p.background : undefined;
  const surfaceIn = isHex(p.surface) ? p.surface : undefined;
  // Light or dark decides how shades are derived and which text colour is used.
  const base = bg ?? surfaceIn;
  const light = base ? isLight(base) : false;

  if (isHex(p.primary)) {
    set("--rp-accent", p.primary);
    set("--rp-on-accent", readableInk(p.primary));
  }
  if (isHex(p.secondary)) set("--rp-violet", p.secondary);
  if (isHex(p.tertiary)) {
    set("--rp-tertiary", p.tertiary);
    set("--rp-teal", p.tertiary);
  }
  for (const k of ["good", "warn", "bad"] as const) {
    const c = p[k];
    if (!isHex(c)) continue;
    set(`--rp-${k}`, c);
    // On dark surfaces the "hi" shade is the light one (text); on light ones it is the dark one.
    set(`--rp-${k}-hi`, light ? mixHex(c, "#000000", 0.25) : mixHex(c, "#ffffff", 0.45));
    set(`--rp-${k}-lo`, light ? mixHex(c, "#000000", 0.45) : mixHex(c, "#000000", 0.2));
  }

  if (bg || surfaceIn) {
    const ink = light ? "#0f172a" : "#e7f1f4";
    const surface = surfaceIn ?? mixHex(bg as string, light ? "#ffffff" : "#ffffff", light ? 0.6 : 0.045);
    const page = bg ?? mixHex(surface, light ? "#000000" : "#000000", light ? 0.04 : 0.28);
    set("--rp-bg", page);
    set("--rp-surface", surface);
    set("--rp-raised", mixHex(surface, ink, 0.06));
    set("--rp-border", mixHex(surface, ink, 0.14));
    set("--rp-inset", mixHex(page, light ? "#000000" : "#000000", light ? 0.05 : 0.3));
    set("--rp-text", ink);
    set("--rp-muted", mixHex(ink, surface, 0.4));
    // The scene behind the panel follows the page colour.
    set("--w-sky-1", mixHex(page, "#000000", light ? 0 : 0.22));
    set("--w-sky-2", mixHex(page, p.primary && isHex(p.primary) ? p.primary : DEFAULT_PALETTE.primary, light ? 0.1 : 0.16));
    set("--w-floor-1", page);
  }
  return out;
};

export type ContrastIssue = {
  /** Which pair: the slot drawn as text/mark, and the one it sits on. */
  pair: [string, string];
  ratio: number;
  min: number;
};

/**
 * Pairs a custom palette can make unreadable. Text colours are derived from the
 * background, so those can only fail on a mid-tone background; the usual
 * problems are an accent or a status colour too close to the surface it is
 * drawn on. Returns only the failing pairs (ratio below the minimum).
 */
export const contrastIssues = (p: Palette): ContrastIssue[] => {
  const eff = { ...DEFAULT_PALETTE, ...Object.fromEntries(Object.entries(p).filter(([, v]) => isHex(v))) } as Record<PaletteKey, string>;
  const vars = paletteVars(p);
  const toHex = (v: string | undefined, fallback: string) => {
    if (!v) return fallback;
    const [r, g, b] = v.split(" ").map(Number);
    return rgbToHex([r, g, b]);
  };
  const surface = toHex(vars["--rp-surface"], eff.surface);
  const page = toHex(vars["--rp-bg"], eff.background);
  const text = toHex(vars["--rp-text"], "#e7f1f4");
  const muted = toHex(vars["--rp-muted"], "#7e97a0");
  const checks: [string, string, string, string, number][] = [
    ["text", "surface", text, surface, 4.5],
    ["text", "background", text, page, 4.5],
    ["muted", "surface", muted, surface, 3],
    ["primary", "surface", eff.primary, surface, 3],
    ["secondary", "surface", eff.secondary, surface, 3],
    ["tertiary", "surface", eff.tertiary, surface, 3],
    ["good", "surface", eff.good, surface, 3],
    ["warn", "surface", eff.warn, surface, 3],
    ["bad", "surface", eff.bad, surface, 3],
    ["surface", "background", surface, page, 1.05],
  ];
  const out: ContrastIssue[] = [];
  for (const [a, b, ca, cb, min] of checks) {
    const ratio = contrastRatio(ca, cb);
    if (ratio < min) out.push({ pair: [a, b], ratio: Math.round(ratio * 100) / 100, min });
  }
  return out;
};
