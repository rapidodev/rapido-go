import { create } from "zustand";
import { PALETTE_KEYS, PALETTE_VARS, Palette, isHex, paletteVars } from "./palette";

// How the panel looks is a handful of independent choices. They are stored in
// localStorage and applied as data-attributes and CSS custom properties on
// <html> - tailwind.css turns those into colours, shadows, radii, motion and so
// on - so changing one never re-renders a page. Only the few components that
// pick between two different elements (a gauge or a bar, a needle or a ring)
// subscribe to the store.

export const THEME_IDS = ["cockpit", "aurora", "graphite", "daylight"] as const;
export type ThemeId = (typeof THEME_IDS)[number];

export const ACCENT_IDS = ["theme", "cyan", "violet", "emerald", "amber", "rose", "sky", "fuchsia"] as const;
export type AccentId = (typeof ACCENT_IDS)[number];

export type Appearance = {
  theme: ThemeId;
  /** "theme" = the preset's own accent. */
  accent: AccentId;
  depth: "off" | "subtle" | "full";
  /** The 3D world behind the panel. */
  environment: "off" | "subtle" | "immersive";
  /** "on" = the immersive/subtle world may be downgraded on weak devices, a low
   * frame rate or reduced motion; "off" = the user picked a level, honour it
   * (reduced motion still wins). Set to "off" when an environment option is chosen. */
  envAuto: "on" | "off";
  /** The pointer (or, on touch, a slow drift) moves the camera. */
  parallax: "on" | "off";
  /** "auto" follows the operating system's prefers-reduced-motion. */
  motion: "auto" | "full" | "reduced" | "none";
  meters: "gauges" | "bars";
  dial: "needle" | "ring";
  density: "comfortable" | "compact";
  radius: "sharp" | "rounded" | "pill";
  /** Custom colours; a slot that is not set keeps the preset's own. Empty by default. */
  palette: Palette;
};

export const DEFAULT_APPEARANCE: Appearance = {
  theme: "cockpit",
  accent: "theme",
  depth: "full",
  environment: "immersive",
  envAuto: "on",
  parallax: "on",
  motion: "auto",
  meters: "gauges",
  dial: "ring",
  density: "comfortable",
  radius: "rounded",
  palette: {},
};

type EnumKey = Exclude<keyof Appearance, "palette">;
const OPTIONS: { [K in EnumKey]: readonly Appearance[K][] } = {
  theme: THEME_IDS,
  accent: ACCENT_IDS,
  depth: ["off", "subtle", "full"],
  environment: ["off", "subtle", "immersive"],
  envAuto: ["on", "off"],
  parallax: ["on", "off"],
  motion: ["auto", "full", "reduced", "none"],
  meters: ["gauges", "bars"],
  dial: ["needle", "ring"],
  density: ["comfortable", "compact"],
  radius: ["sharp", "rounded", "pill"],
};

export const APPEARANCE_OPTIONS = OPTIONS;

// Space-separated RGB triples, which is what `rgb(var(--rp-accent) / .5)` and
// Tailwind's `<alpha-value>` both need. The lighter / darker / edge shades the
// 3D key and the dial are built from are not listed: tailwind.css derives them
// from --rp-accent with color-mix. `lo` is the darker accent used as the base
// on the light preset; `on` is the ink drawn on the accent.
type AccentShades = { accent: string; hi: string; lo: string; edge: string; on: string };
export const ACCENTS: Record<Exclude<AccentId, "theme">, AccentShades> = {
  cyan: { accent: "34 211 238", hi: "124 234 255", lo: "15 182 211", edge: "11 116 144", on: "3 34 43" },
  violet: { accent: "167 139 250", hi: "221 214 254", lo: "124 58 237", edge: "76 29 149", on: "20 8 50" },
  emerald: { accent: "52 211 153", hi: "134 239 172", lo: "5 150 105", edge: "4 95 70", on: "2 32 22" },
  amber: { accent: "251 191 36", hi: "253 224 71", lo: "217 119 6", edge: "146 64 14", on: "40 20 0" },
  rose: { accent: "251 113 133", hi: "253 164 175", lo: "225 29 72", edge: "136 19 55", on: "50 5 15" },
  sky: { accent: "56 189 248", hi: "125 211 252", lo: "2 132 199", edge: "3 105 161", on: "3 25 45" },
  fuchsia: { accent: "232 121 249", hi: "240 171 252", lo: "192 38 211", edge: "134 25 143", on: "45 5 55" },
};

/** Preview colour of each swatch, for the picker. */
export const ACCENT_SWATCH: Record<Exclude<AccentId, "theme">, string> = Object.fromEntries(
  Object.entries(ACCENTS).map(([k, v]) => [k, `rgb(${v.accent})`])
) as Record<Exclude<AccentId, "theme">, string>;

/** Browser chrome colour per preset. */
export const THEME_COLOR: Record<ThemeId, string> = {
  cockpit: "#070d12",
  aurora: "#09081a",
  graphite: "#111214",
  daylight: "#eef2f7",
};

export const STORAGE_KEY = "rapido.appearance";

const pick = <K extends EnumKey>(key: K, raw: unknown): Appearance[K] =>
  (OPTIONS[key] as readonly unknown[]).includes(raw) ? (raw as Appearance[K]) : DEFAULT_APPEARANCE[key];

export const sanitizePalette = (raw: unknown): Palette => {
  const out: Palette = {};
  if (raw && typeof raw === "object") {
    for (const k of PALETTE_KEYS) {
      const v = (raw as Record<string, unknown>)[k];
      if (isHex(v)) out[k] = v.toLowerCase();
    }
  }
  return out;
};

/** Whatever was stored, coerced to a valid Appearance - unknown keys dropped,
 * unknown values falling back to the default for that field. */
export const sanitizeAppearance = (raw: unknown): Appearance => {
  const src = raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {};
  const out = {} as Record<string, unknown>;
  for (const key of Object.keys(OPTIONS) as EnumKey[]) out[key] = pick(key, src[key]);
  out.palette = sanitizePalette(src.palette);
  return out as Appearance;
};

export const loadAppearance = (): Appearance => {
  try {
    const text = window.localStorage.getItem(STORAGE_KEY);
    return sanitizeAppearance(text ? JSON.parse(text) : null);
  } catch {
    return { ...DEFAULT_APPEARANCE, palette: {} };
  }
};

/** Has this visitor chosen their own look? Only then does it beat the panel default. */
export const hasPersonalOverride = (): boolean => {
  try {
    return window.localStorage.getItem(STORAGE_KEY) !== null;
  } catch {
    return false;
  }
};

export const saveAppearance = (a: Appearance): void => {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(a));
  } catch {
    // Private mode / blocked storage: the choice just lasts until reload.
  }
};

export const systemPrefersReducedMotion = (): boolean => {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return true;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
};

/** What "motion" the page actually runs: auto resolves to the OS setting. */
export const resolveMotion = (motion: Appearance["motion"], systemReduced: boolean): "full" | "reduced" | "none" =>
  motion === "auto" ? (systemReduced ? "reduced" : "full") : motion;

const ACCENT_VARS = ["--rp-accent", "--rp-on-accent"] as const;

// A custom palette is written over whatever the preset and accent set. Cleared first so
// removing a colour really returns the preset's own.
const applyPalette = (root: HTMLElement, palette: Palette) => {
  for (const v of PALETTE_VARS) if (v !== "--rp-accent" && v !== "--rp-on-accent") root.style.removeProperty(v);
  for (const [k, v] of Object.entries(paletteVars(palette))) root.style.setProperty(k, v);
};

/** Writes the appearance onto <html>. Pure DOM, no React. */
export const applyAppearance = (
  a: Appearance,
  root: HTMLElement = document.documentElement,
  systemReduced: boolean = systemPrefersReducedMotion()
): void => {
  root.setAttribute("data-theme", a.theme);
  root.setAttribute("data-depth", a.depth);
  root.setAttribute("data-environment", a.environment);
  root.setAttribute("data-parallax", a.parallax);
  root.setAttribute("data-motion", resolveMotion(a.motion, systemReduced));
  root.setAttribute("data-meters", a.meters);
  root.setAttribute("data-dial", a.dial);
  root.setAttribute("data-density", a.density);
  root.setAttribute("data-radius", a.radius);

  if (a.accent === "theme") {
    for (const v of ACCENT_VARS) root.style.removeProperty(v);
  } else {
    const s = ACCENTS[a.accent];
    // On the light preset the accent is also used as text on white, so the
    // darker shade stands in for the base colour to keep it readable.
    const base = a.theme === "daylight" ? s.lo : s.accent;
    root.style.setProperty("--rp-accent", base);
    root.style.setProperty("--rp-on-accent", a.theme === "daylight" ? "255 255 255" : s.on);
  }

  applyPalette(root, a.palette);

  try {
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute("content", isHex(a.palette.background) ? a.palette.background : THEME_COLOR[a.theme]);
  } catch {
    // no document (tests without jsdom): nothing to tint
  }
};

/** Where the look currently in force came from. */
export type AppearanceSource = "personal" | "panel" | "default";

type AppearanceState = Appearance & {
  source: AppearanceSource;
  /** Make a choice of my own: applied and remembered in this browser (it now beats the panel default). */
  set: (patch: Partial<Appearance>) => void;
  /** Try a change: applied at once, remembered nowhere. For live previews that can be reverted. */
  preview: (patch: Partial<Appearance>) => void;
  /** Drop my own choices and go back to the panel default (or the built-in look if there is none). */
  reset: () => void;
};

// The panel-wide default, when this browser has been told one. It is what a visitor
// with no choice of their own gets, and what "reset" returns to.
let panelDefault: Appearance | null = null;
export const getPanelDefault = (): Appearance | null => panelDefault;

const fallbackLook = (): Appearance => (panelDefault ? { ...panelDefault, palette: { ...panelDefault.palette } } : { ...DEFAULT_APPEARANCE, palette: {} });

export const useAppearance = create<AppearanceState>((set, get) => {
  const personal = hasPersonalOverride();
  return {
    ...loadAppearance(),
    source: personal ? "personal" : "default",
    set: (patch) => {
      const next = sanitizeAppearance({ ...pickValues(get()), ...patch });
      set({ ...next, source: "personal" });
      saveAppearance(next);
      applyAppearance(next);
    },
    preview: (patch) => {
      const next = sanitizeAppearance({ ...pickValues(get()), ...patch });
      set(next);
      applyAppearance(next);
    },
    reset: () => {
      const next = fallbackLook();
      set({ ...next, source: panelDefault ? "panel" : "default" });
      try {
        window.localStorage.removeItem(STORAGE_KEY);
      } catch {
        // see saveAppearance
      }
      applyAppearance(next);
    },
  };
});

function pickValues(s: Appearance): Appearance {
  const out = {} as Record<string, unknown>;
  for (const k of Object.keys(OPTIONS)) out[k] = (s as unknown as Record<string, unknown>)[k];
  out.palette = { ...s.palette };
  return out as unknown as Appearance;
}

export const currentAppearance = (): Appearance => pickValues(useAppearance.getState());

/**
 * Tell the app what the panel default is (from the cache at startup, then from the
 * server). A visitor's own saved choice always wins; with none, the look becomes
 * the panel default. Pass null when the panel has no default.
 */
export const setPanelDefault = (a: Appearance | null): void => {
  panelDefault = a;
  if (hasPersonalOverride()) return;
  const next = fallbackLook();
  useAppearance.setState({ ...next, source: a ? "panel" : "default" });
  applyAppearance(next);
};

/** Call once at startup: applies the look in force (mine, else the cached panel default, else the
 * built-in one) and keeps "auto" motion following the operating system if it changes. */
export const initAppearance = (cachedPanelDefault?: Appearance | null): void => {
  if (cachedPanelDefault !== undefined) panelDefault = cachedPanelDefault;
  if (hasPersonalOverride()) {
    useAppearance.setState({ ...loadAppearance(), source: "personal" });
  } else {
    const next = fallbackLook();
    useAppearance.setState({ ...next, source: panelDefault ? "panel" : "default" });
  }
  applyAppearance(currentAppearance());
  if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const onChange = () => applyAppearance(currentAppearance());
    if (mq.addEventListener) mq.addEventListener("change", onChange);
    else mq.addListener(onChange);
  }
};

// Recharts takes plain colour strings (SVG attributes cannot hold var()), so
// the Overview charts read their colours from here: the same values as the
// theme tokens in tailwind.css, per preset.
const THEME_ACCENT: Record<ThemeId, string> = {
  cockpit: "34 211 238",
  aurora: "192 132 252",
  graphite: "96 165 250",
  daylight: "8 145 178",
};

export type ChartPalette = {
  accent: string;
  grid: string;
  tick: string;
  tipBg: string;
  tipBorder: string;
  tipText: string;
  /** The status trio and the secondary brand hue, as plain colours for libraries that cannot read CSS variables. */
  good: string;
  warn: string;
  bad: string;
  warm: string;
  violet: string;
  muted: string;
};

const DARK_STATUS = { good: "#36eab0", warn: "#ffbe4a", bad: "#ff6870", warm: "#ff9454", violet: "#a78bfa" };
const CHART_BASE: Record<ThemeId, Omit<ChartPalette, "accent">> = {
  cockpit: { grid: "#223039", tick: "#7e97a0", tipBg: "#111c22", tipBorder: "#223039", tipText: "#e7f1f4", muted: "#7e97a0", ...DARK_STATUS },
  aurora: { grid: "#3e3876", tick: "#a09ecd", tipBg: "#181538", tipBorder: "#3e3876", tipText: "#eeeeff", muted: "#a09ecd", ...DARK_STATUS },
  graphite: { grid: "#363840", tick: "#969ba5", tipBg: "#1b1c1f", tipBorder: "#363840", tipText: "#e5e7eb", muted: "#969ba5", ...DARK_STATUS },
  daylight: { grid: "#cbd5e1", tick: "#57687e", tipBg: "#ffffff", tipBorder: "#cbd5e1", tipText: "#0f172a", muted: "#57687e", good: "#047857", warn: "#a16207", bad: "#c81e1e", warm: "#c2410c", violet: "#6d28d9" },
};

export const chartPalette = (theme: ThemeId, accent: AccentId, palette: Palette = {}): ChartPalette => {
  const triple =
    accent === "theme" ? THEME_ACCENT[theme] : theme === "daylight" ? ACCENTS[accent].lo : ACCENTS[accent].accent;
  const base = CHART_BASE[theme];
  return {
    ...base,
    accent: isHex(palette.primary) ? palette.primary : `rgb(${triple})`,
    good: isHex(palette.good) ? palette.good : base.good,
    warn: isHex(palette.warn) ? palette.warn : base.warn,
    bad: isHex(palette.bad) ? palette.bad : base.bad,
    violet: isHex(palette.secondary) ? palette.secondary : base.violet,
    tipBg: isHex(palette.surface) ? palette.surface : base.tipBg,
  };
};

export const useChartPalette = (): ChartPalette => {
  const theme = useAppearance((s) => s.theme);
  const accent = useAppearance((s) => s.accent);
  const palette = useAppearance((s) => s.palette);
  return chartPalette(theme, accent, palette);
};
