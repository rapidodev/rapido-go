// ============================================================================
// RAPIDO INSTRUMENT LANGUAGE
// ----------------------------------------------------------------------------
// One small design language for every dial, bar, trend, pill, pipe and chart
// in the panel. It is "inspired by a speedometer", not a copy of one: no chrome,
// no skeuomorphic knobs - a flat, modern, HUD-like read-out built from ONE
// segment idea. If you add a visual, take its values from here.
//
// GEOMETRY   An open ring: ARC.sweep degrees with the gap at the bottom. The
//            ring is a row of small ROUNDED DASHES (the "segment"), not a
//            continuous stroke. The same segment is reused for bars
//            (SegmentBar) and trends (TrendBars): a row of rounded dashes that
//            light up to the value. Dash count: 48 (32 on small dials). One
//            stroke scale: DASH.width (set by spacing), DASH.length, 1px hairlines
//            for plates, 2px for pipes. Caps are always round.
// COLOUR     Zone ramp good -> warn -> bad, blended smoothly across each
//            boundary (never hard steps): --rp-good / --rp-warn / --rp-bad.
//            Brand accents --rp-accent (primary, e.g. download) and --rp-violet
//            (secondary, e.g. upload). An unlit segment is always --rp-unlit.
//            The same tokens colour dials, bars, trends, pills, badges, HAProxy
//            pipes and the Overview charts.
// GLOW       One recipe: glow(color) = a 3.5px drop-shadow of the zone colour at
//            ~80%, on lit segments only. Text never glows. A "live" thing
//            pulses with the PulseDot ping; "traffic" moves with rp-flow dots.
// TYPE       The read-out is TYPE.readout (big tabular numeral) over TYPE.unit
//            (small, muted), with TYPE.label (quiet, medium) and TYPE.caption
//            under it. Stat tiles, dials and chart axes all use these.
// MOTION     One easing (MOTION.ease, slight overshoot) and one duration
//            (MOTION.ms) for every value change - the arc, the head, the bar
//            fill. Honours the Appearance motion setting.
// SURFACE    page (bg) < card (surface, elevation 2) < raised < inset
//            (instrument wells, plates, inputs, chart beds). Cards, cluster
//            zones, modals and inputs share these steps, the --rp-r-* radii
//            and 1px --rp-border hairlines.
// ============================================================================

export type GaugeZone = {
  /** Where the zone ends, as a fraction 0..1 of the min..max range. Ascending. */
  upTo: number;
  color: string;
};

// ---- colour tokens ---------------------------------------------------------
// CSS colours (applied through style, never as SVG attributes, where var() is
// not valid). They follow the active theme preset and accent.
export const GAUGE_GOOD = "rgb(var(--rp-good))";
export const GAUGE_WARN = "rgb(var(--rp-warn))";
export const GAUGE_BAD = "rgb(var(--rp-bad))";
export const GAUGE_CYAN = "rgb(var(--rp-accent))";
export const GAUGE_VIOLET = "rgb(var(--rp-violet))";
export const GAUGE_TEAL = "rgb(var(--rp-teal))";
export const UNLIT = "rgb(var(--rp-unlit))";

/** Mint, then amber from 70%, coral from 90% - the usual "how loaded is it" read. */
export const LOAD_ZONES: GaugeZone[] = [
  { upTo: 0.7, color: GAUGE_GOOD },
  { upTo: 0.9, color: GAUGE_WARN },
  { upTo: 1, color: GAUGE_BAD },
];

/** One colour end to end, for readings that are not good-or-bad (traffic, counts). */
export const solidZones = (color: string): GaugeZone[] => [{ upTo: 1, color }];

/** A brand colour that turns warn/bad only when it nears its limit. */
export const saturatingZones = (color: string): GaugeZone[] => [
  { upTo: 0.8, color },
  { upTo: 0.93, color: GAUGE_WARN },
  { upTo: 1, color: GAUGE_BAD },
];

// ---- geometry ----------------------------------------------------------------
export const ARC = { start: 145, sweep: 250 } as const;
export const CENTER = 100;
export const dashCount = (compact: boolean) => (compact ? 32 : 48);

// ---- motion ------------------------------------------------------------------
export const MOTION = {
  ease: "cubic-bezier(.34,1.32,.5,1)",
  ms: 900,
} as const;

// ---- typography (whole class strings, so Tailwind finds them) -----------------
export const TYPE = {
  readout: "font-semibold leading-none tracking-tight tabular-nums text-rapido-text",
  unit: "leading-none text-rapido-muted",
  label: "text-xs font-medium text-rapido-text",
  caption: "text-[11px] leading-tight text-rapido-muted",
  axis: "text-[11px] tabular-nums text-rapido-muted",
} as const;

// ---- maths -------------------------------------------------------------------
export const fractionOf = (value: number, min: number, max: number): number => {
  const span = max - min;
  if (!(span > 0) || !Number.isFinite(value)) return 0;
  return Math.min(1, Math.max(0, (value - min) / span));
};

export const zoneColorAt = (zones: readonly GaugeZone[], f: number): string => {
  for (const z of zones) if (f <= z.upTo + 1e-9) return z.color;
  return zones[zones.length - 1]?.color ?? GAUGE_CYAN;
};

// color-mix is what lets the ramp sweep smoothly from good to warning to bad.
// Where the browser lacks it the colours step at the zone edges instead.
const supportsMix = (() => {
  try {
    return (
      typeof CSS !== "undefined" &&
      typeof CSS.supports === "function" &&
      CSS.supports("color", "color-mix(in oklab, red 50%, blue)")
    );
  } catch {
    return false;
  }
})();

export const mixColor = (a: string, b: string, t: number): string => {
  if (!supportsMix) return t < 0.5 ? a : b;
  if (t <= 0) return a;
  if (t >= 1) return b;
  return `color-mix(in oklab, ${a} ${Math.round((1 - t) * 100)}%, ${b})`;
};

export const withAlpha = (color: string, alpha: number): string =>
  supportsMix ? `color-mix(in srgb, ${color} ${Math.round(alpha * 100)}%, transparent)` : color;

/** Like zoneColorAt, but blended across each zone boundary (over `blend` of the
 * range, centred on it) so the colour sweeps instead of stepping. */
export const smoothColorAt = (zones: readonly GaugeZone[], f: number, blend = 0.2): string => {
  for (let i = 0; i < zones.length - 1; i++) {
    const b = zones[i].upTo;
    const lo = b - blend / 2;
    if (f >= lo && f <= b + blend / 2) return mixColor(zones[i].color, zones[i + 1].color, (f - lo) / blend);
  }
  return zoneColorAt(zones, f);
};

/** The glow recipe, as a CSS filter, for SVG groups of lit segments. */
export const glow = (color: string): string => `drop-shadow(0 0 3.5px ${withAlpha(color, 0.8)})`;
/** The same recipe for an HTML element's box-shadow. */
export const glowShadow = (color: string): string => `0 0 7px ${withAlpha(color, 0.75)}`;

/** 1200 -> "1.2k", 3_500_000 -> "3.5M", 40 -> "40". For axis and scale labels. */
export const formatCompact = (n: number): string => {
  const abs = Math.abs(n);
  const trim = (v: number) => String(parseFloat(v.toFixed(1)));
  if (abs >= 1e9) return `${trim(n / 1e9)}G`;
  if (abs >= 1e6) return `${trim(n / 1e6)}M`;
  if (abs >= 1e3) return `${trim(n / 1e3)}k`;
  return trim(n);
};

/** How many of `count` segments are lit for a fraction: any reading above zero
 * lights at least the first, a full scale lights them all. */
export const litSegments = (f: number, count: number): number => {
  if (!(f > 0)) return 0;
  return Math.min(count, Math.max(1, Math.round(f * count)));
};
