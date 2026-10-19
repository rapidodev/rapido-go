import { create } from "zustand";
import { ACCENTS, Appearance, DEFAULT_APPEARANCE, ThemeId, THEME_IDS, setPanelDefault } from "./appearance";
import { PALETTE_KEYS, Palette, PaletteKey, isHex } from "./palette";

// The panel-wide default theme the server stores (GET/PUT/DELETE
// /settings/theme) and its translation to and from this browser's Appearance.
// The server shape is flat enum strings plus hex colours; every key is optional.

export type PanelTheme = {
  preset?: string;
  /** #rrggbb - the primary colour; the palette's `primary` wins if both are given. */
  accent?: string;
  palette?: Palette;
  depth?: string;
  environment?: string;
  gauge?: string;
  meters?: string;
  motion?: string;
  density?: string;
  radius?: string;
  background?: string;
  parallax?: string;
};

export type AllowedMap = Record<string, string[]>;

export type PanelThemeResponse = {
  theme: PanelTheme;
  updated_at?: string | null;
  updated_by?: string | null;
  allowed?: AllowedMap;
};

/** The option lists, used when the server does not send its own `allowed` map. */
export const DEFAULT_ALLOWED: AllowedMap = {
  preset: [...THEME_IDS],
  depth: ["off", "subtle", "full"],
  environment: ["off", "subtle", "immersive"],
  gauge: ["ring", "needle"],
  meters: ["gauges", "bars"],
  motion: ["auto", "full", "reduced", "none"],
  density: ["comfortable", "compact"],
  radius: ["sharp", "rounded", "pill"],
  background: ["on", "off"],
  parallax: ["on", "off"],
};

const ENUM_KEYS = ["preset", "depth", "environment", "gauge", "meters", "motion", "density", "radius", "background", "parallax"] as const;
const KNOWN_KEYS = new Set<string>([...ENUM_KEYS, "accent", "palette"]);

export type ThemeValidation = { ok: true; theme: PanelTheme } | { ok: false; errors: string[] };

/**
 * Checks a theme before it is applied or sent: an object, only known keys, every
 * enum value in its allowed list, every colour #rrggbb. Nothing is coerced - a
 * bad value is reported, so an import never half-applies.
 */
export const validatePanelTheme = (raw: unknown, allowed: AllowedMap = DEFAULT_ALLOWED): ThemeValidation => {
  const errors: string[] = [];
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return { ok: false, errors: ["not an object"] };
  // Accept both a bare theme and the {theme: {...}} wrapper the API uses.
  const src = (raw as Record<string, unknown>).theme && typeof (raw as Record<string, unknown>).theme === "object" && !Array.isArray((raw as Record<string, unknown>).theme)
    ? ((raw as Record<string, unknown>).theme as Record<string, unknown>)
    : (raw as Record<string, unknown>);
  const out: PanelTheme = {};
  for (const [k, v] of Object.entries(src)) {
    if (!KNOWN_KEYS.has(k)) {
      errors.push(`unknown key: ${k}`);
      continue;
    }
    if (k === "accent") {
      if (isHex(v)) out.accent = v.toLowerCase();
      else errors.push("accent: expected #rrggbb");
    } else if (k === "palette") {
      if (!v || typeof v !== "object" || Array.isArray(v)) {
        errors.push("palette: expected an object");
        continue;
      }
      const pal: Palette = {};
      for (const [pk, pv] of Object.entries(v as Record<string, unknown>)) {
        if (!(PALETTE_KEYS as readonly string[]).includes(pk)) errors.push(`palette.${pk}: unknown colour`);
        else if (isHex(pv)) pal[pk as PaletteKey] = pv.toLowerCase();
        else errors.push(`palette.${pk}: expected #rrggbb`);
      }
      out.palette = pal;
    } else {
      const list = allowed[k] ?? DEFAULT_ALLOWED[k];
      if (typeof v === "string" && list.includes(v)) (out as Record<string, unknown>)[k] = v;
      else errors.push(`${k}: expected one of ${list.join(", ")}`);
    }
  }
  return errors.length ? { ok: false, errors } : { ok: true, theme: out };
};

/** A server theme as an Appearance, on top of `base` (what the keys it omits fall back to). */
export const panelThemeToAppearance = (t: PanelTheme, base: Appearance = DEFAULT_APPEARANCE): Appearance => {
  const a: Appearance = { ...base, palette: { ...base.palette } };
  if (t.preset && (THEME_IDS as readonly string[]).includes(t.preset)) a.theme = t.preset as ThemeId;
  if (t.depth === "off" || t.depth === "subtle" || t.depth === "full") a.depth = t.depth;
  if (t.motion === "auto" || t.motion === "full" || t.motion === "reduced" || t.motion === "none") a.motion = t.motion;
  if (t.meters === "gauges" || t.meters === "bars") a.meters = t.meters;
  if (t.gauge === "ring" || t.gauge === "needle") a.dial = t.gauge;
  if (t.density === "comfortable" || t.density === "compact") a.density = t.density;
  if (t.radius === "sharp" || t.radius === "rounded" || t.radius === "pill") a.radius = t.radius;
  if (t.parallax === "on" || t.parallax === "off") a.parallax = t.parallax;
  if (t.environment === "off" || t.environment === "subtle" || t.environment === "immersive") a.environment = t.environment;
  // The old "animated background" switch: off means no scene at all.
  if (t.background === "off") a.environment = "off";
  // A server default is a default: it may be stepped down on a weak device, a user's own pick may not.
  a.envAuto = "on";
  const pal: Palette = { ...(t.palette ?? {}) };
  if (isHex(t.accent) && !pal.primary) pal.primary = t.accent;
  a.palette = pal;
  if (pal.primary) a.accent = "theme";
  return a;
};

/** An Appearance as a server theme: every key, and only the colours that are really set. */
export const appearanceToPanelTheme = (a: Appearance): PanelTheme => {
  const palette: Palette = {};
  for (const k of PALETTE_KEYS) {
    const v = a.palette[k];
    if (isHex(v)) palette[k] = v.toLowerCase();
  }
  // A picked accent swatch is sent as the accent colour.
  const swatch = a.accent !== "theme" ? ACCENTS[a.accent]?.accent : undefined;
  const accentHex = swatch ? `#${swatch.split(" ").map((n) => Number(n).toString(16).padStart(2, "0")).join("")}` : undefined;
  const theme: PanelTheme = {
    preset: a.theme,
    depth: a.depth,
    environment: a.environment,
    gauge: a.dial,
    meters: a.meters,
    motion: a.motion,
    density: a.density,
    radius: a.radius,
    background: a.environment === "off" ? "off" : "on",
    parallax: a.parallax,
  };
  if (Object.keys(palette).length) theme.palette = palette;
  const accent = palette.primary ?? accentHex;
  if (accent) theme.accent = accent;
  return theme;
};

/** The last panel theme this browser saw, so the first paint is not the built-in look. */
export const PANEL_THEME_CACHE_KEY = "rapido.panelTheme";

export const readPanelThemeCache = (): PanelTheme | null => {
  try {
    const text = window.localStorage.getItem(PANEL_THEME_CACHE_KEY);
    if (!text) return null;
    const v = validatePanelTheme(JSON.parse(text));
    return v.ok ? v.theme : null;
  } catch {
    return null;
  }
};

export const writePanelThemeCache = (t: PanelTheme | null): void => {
  try {
    if (t) window.localStorage.setItem(PANEL_THEME_CACHE_KEY, JSON.stringify(t));
    else window.localStorage.removeItem(PANEL_THEME_CACHE_KEY);
  } catch {
    // nothing to remember it in
  }
};

// The panel default as a small store, so the Theme page and the sync agree on it.
export const usePanelTheme = create<{ theme: PanelTheme | null; set: (t: PanelTheme | null) => void }>((set) => ({
  theme: readPanelThemeCache(),
  set: (theme) => set({ theme }),
}));

export const isEmptyTheme = (t: PanelTheme | null | undefined): boolean => !t || Object.keys(t).length === 0;

/**
 * What the server said the panel default is: remembered (so the next first paint already has it),
 * and made the look of every visitor who has not chosen their own. An empty theme means the panel
 * has no default and the built-in look applies.
 */
export const applyServerTheme = (theme: PanelTheme | null | undefined): void => {
  const t = isEmptyTheme(theme) ? null : (theme as PanelTheme);
  usePanelTheme.getState().set(t);
  writePanelThemeCache(t);
  setPanelDefault(t ? panelThemeToAppearance(t) : null);
};

/** The cached panel default as an Appearance, for the very first paint. */
export const cachedPanelAppearance = (): Appearance | null => {
  const t = readPanelThemeCache();
  return t ? panelThemeToAppearance(t) : null;
};
