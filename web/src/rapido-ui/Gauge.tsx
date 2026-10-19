import { CSSProperties, FC, ReactNode, useId } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { CountUp } from "./CountUp";
import { useAppearance } from "./appearance";
import {
  CENTER as C,
  GaugeZone,
  LOAD_ZONES,
  MOTION,
  TYPE,
  dashCount,
  formatCompact,
  fractionOf,
  glow,
  litSegments,
  smoothColorAt,
  zoneColorAt,
} from "./instrument";
import { DashRing, Plate, arcPath, point, rotateOf, toDeg, useSweep } from "./DashRing";
import { useReducedMotion } from "./motion";

// The panel's dial, written in the Rapido instrument language (see
// instrument.ts): an open ring of small rounded dashes that light up to the
// value in a smoothly blended good -> warn -> bad ramp, a bright head on the
// last lit dash, a flat plate behind it, and a big tabular read-out in the
// middle. "needle" swaps the head for a slim needle on the same arc.
// The arc and the head move on the same easing, and the number counts up from
// a ref - a new reading costs one React render and no per-frame state.

// Re-exported so call sites keep importing the colour tokens from the dial.
export type { GaugeZone };
export {
  GAUGE_BAD,
  GAUGE_CYAN,
  GAUGE_GOOD,
  GAUGE_TEAL,
  GAUGE_VIOLET,
  GAUGE_WARN,
  LOAD_ZONES,
  formatCompact,
  fractionOf,
  smoothColorAt,
  solidZones,
  zoneColorAt,
} from "./instrument";

const SIZES = { sm: 112, md: 164, lg: 224 } as const;
export type GaugeSize = keyof typeof SIZES | number;

/** The pixel width a size name stands for. */
export const gaugePx = (size: GaugeSize): number => (typeof size === "number" ? size : SIZES[size]);

export { arcPath };

export type GaugeProps = {
  /** null / undefined / NaN = unknown: the arc stays dark and the readout shows a dash. */
  value: number | null | undefined;
  min?: number;
  max: number;
  unit?: string | ((value: number) => string);
  label?: ReactNode;
  /** Plain-text label for the accessible name when `label` is not a string. */
  ariaLabel?: string;
  sublabel?: ReactNode;
  zones?: readonly GaugeZone[];
  size?: GaugeSize;
  /** A highest-seen marker on the rim. */
  peak?: number | null;
  decimals?: number;
  /** Full control of the number shown (bytes, bits per second). Return the number part only; put the unit in `unit`. */
  formatValue?: (value: number) => string;
  /** Label text for the two ends of the scale (needle dial), defaults to a compact number. */
  tickFormat?: (value: number) => string;
  /** Kept for call-site compatibility; the language has no numbered scale. */
  ticks?: number;
  /** "ring" = lit dashes round a big readout, "needle" = a slim needle on the same arc. Defaults to the Appearance setting. */
  dial?: "needle" | "ring";
  loading?: boolean;
  className?: string;
};

// Geometry of the dash ring (viewBox units).
const R_IN = 70;
const R_OUT = 90;
const R_MID = (R_IN + R_OUT) / 2;

export const Gauge: FC<GaugeProps> = ({
  value,
  min = 0,
  max,
  unit,
  label,
  ariaLabel,
  sublabel,
  zones = LOAD_ZONES,
  size = "md",
  peak,
  decimals = 0,
  formatValue,
  tickFormat = formatCompact,
  dial: dialProp,
  loading = false,
  className,
}) => {
  const { t } = useTranslation();
  const uid = useId().replace(/[^a-zA-Z0-9]/g, "");
  const id = (n: string) => `${n}${uid}`;
  const reduced = useReducedMotion();
  const storeDial = useAppearance((s) => s.dial);
  const ring = (dialProp ?? storeDial) === "ring";
  const px = typeof size === "number" ? size : SIZES[size];
  const compact = px < 140;
  const N = dashCount(compact);

  const known = !loading && typeof value === "number" && Number.isFinite(value);
  const f = known ? fractionOf(value as number, min, max) : 0;
  const lit = litSegments(f, N);
  const target = lit / N; // the arc always ends between two dashes

  const shown = useSweep(target, reduced);

  const color = smoothColorAt(zones, known ? f : 0);
  const fmt = (v: number) => (formatValue ? formatValue(v) : v.toFixed(Math.max(0, decimals)));
  const unitText = known ? (typeof unit === "function" ? unit(value as number) : unit) : undefined;

  const name = ariaLabel ?? (typeof label === "string" ? label : "");
  let accessible: string;
  if (loading) accessible = String(t("rapido.gauge.loading", { label: name }));
  else if (!known) accessible = String(t("rapido.gauge.noData", { label: name }));
  else {
    const base = String(
      t("rapido.gauge.reading", { label: name, value: fmt(value as number), unit: unitText ?? "" })
    ).trim();
    accessible =
      peak != null && Number.isFinite(peak) ? `${base}, ${t("rapido.gauge.peak", { value: fmt(peak) })}` : base;
  }

  const dashW = Math.min(compact ? 6.2 : 4.6, ((R_IN + R_OUT) / 2) * 4.363 / N * 0.62);
  const moveT = reduced ? "none" : `transform ${MOTION.ms}ms ${MOTION.ease}`;
  const peakOk = peak != null && Number.isFinite(peak);
  const peakF = peakOk ? fractionOf(peak as number, min, max) : 0;

  // ---- the read-out -----------------------------------------------------------
  const text = known ? fmt(value as number) : "—";
  const lenScale = text.length >= 6 ? 0.72 : text.length >= 5 ? 0.82 : text.length >= 4 ? 0.92 : 1;
  const valueSize = Math.round(px * (ring ? (compact ? 0.26 : 0.235) : compact ? 0.19 : 0.17) * lenScale);
  const unitSize = Math.max(9, Math.round(px * 0.065));
  const dial: CSSProperties = { width: px, height: px };
  const [minX, minY] = point(R_IN - 9, toDeg(0.02));
  const [maxX, maxY] = point(R_IN - 9, toDeg(0.98));

  return (
    <div
      role="img"
      aria-label={accessible}
      data-fraction={known ? f.toFixed(4) : undefined}
      data-state={loading ? "loading" : known ? "ready" : "unknown"}
      dir="ltr"
      className={classNames("inline-flex max-w-full flex-col items-center", className)}
    >
      <div className="relative" style={dial}>
        <svg viewBox="0 0 200 200" width="100%" height="100%" aria-hidden="true" focusable="false" className="overflow-visible">
          <Plate uid={uid} />

          <DashRing
            uid={uid}
            rIn={R_IN}
            rOut={R_OUT}
            count={N}
            dashW={dashW}
            zones={zones}
            shown={shown}
            color={color}
            reduced={reduced}
            head={ring && known}
            flat={compact}
            testId="gauge-head"
          />

          {/* highest-seen marker: a thin tick reaching past both edges of the ring, with a pointer */}
          {peakOk && (
            <g
              data-testid="gauge-peak"
              style={{ transform: rotateOf(peakF), transformOrigin: `${C}px ${C}px`, transition: moveT }}
            >
              <line
                x1={C}
                y1={C - (R_IN - 5)}
                x2={C}
                y2={C - (R_OUT + 5)}
                strokeLinecap="round"
                strokeWidth={1.8}
                style={{ stroke: "rgb(var(--rp-text))" }}
                opacity={0.95}
              />
              <path d={`M${C - 3.4} ${C - R_OUT - 11} L${C + 3.4} ${C - R_OUT - 11} L${C} ${C - R_OUT - 5.5} Z`} style={{ fill: "rgb(var(--rp-text))" }} />
            </g>
          )}

          {/* needle variant: a slim needle on the same arc, and the two scale ends */}
          {!ring && (
            <>
              <text x={minX} y={minY} textAnchor="middle" dominantBaseline="central" fontSize={9} style={{ fill: "rgb(var(--rp-muted))" }}>
                {compact ? "" : tickFormat(min)}
              </text>
              <text x={maxX} y={maxY} textAnchor="middle" dominantBaseline="central" fontSize={9} style={{ fill: "rgb(var(--rp-muted))" }}>
                {compact ? "" : tickFormat(max)}
              </text>
              <g
                data-testid="gauge-needle"
                style={{
                  transform: rotateOf(shown),
                  transformOrigin: `${C}px ${C}px`,
                  transformBox: "view-box",
                  transition: moveT,
                  filter: glow(color),
                }}
              >
                <line x1={C} y1={C} x2={C} y2={C - (R_IN - 6)} strokeLinecap="round" strokeWidth={2.4} style={{ stroke: "rgb(var(--rp-text))" }} />
              </g>
              <circle cx={C} cy={C} r={5.5} style={{ fill: "rgb(var(--rp-text))" }} />
              <circle cx={C} cy={C} r={2.2} style={{ fill: "rgb(var(--rp-inset))" }} />
            </>
          )}
        </svg>

        {/* The read-out: the number big, the unit small under it. */}
        <div
          className="pointer-events-none absolute inset-x-0 flex flex-col items-center gap-1"
          style={ring ? { top: "50%", transform: "translateY(-50%)" } : { top: compact ? "58%" : "60%" }}
        >
          <span
            className={classNames(TYPE.readout, loading && "animate-pulse")}
            style={{ fontSize: valueSize }}
            data-testid="gauge-value"
          >
            {known ? <CountUp value={value as number} format={fmt} duration={reduced ? 0 : MOTION.ms} /> : "—"}
          </span>
          {unitText && (
            <span className={TYPE.unit} style={{ fontSize: unitSize }}>
              {unitText}
            </span>
          )}
        </div>
      </div>
      {(label || sublabel) && (
        <div className="mt-1.5 max-w-full text-center">
          {label && <div className={classNames(TYPE.label, "truncate")}>{label}</div>}
          {sublabel && <div className={classNames(TYPE.caption, "mt-0.5")}>{sublabel}</div>}
        </div>
      )}
    </div>
  );
};
