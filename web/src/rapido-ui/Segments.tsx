import { CSSProperties, FC } from "react";
import classNames from "classnames";
import {
  GaugeZone,
  LOAD_ZONES,
  TYPE,
  UNLIT,
  fractionOf,
  glowShadow,
  litSegments,
  smoothColorAt,
} from "./instrument";

// The instrument language's segment, on a straight line: a row of small
// rounded dashes that light up to the value (SegmentBar - the classic bar), and
// a row of dash-wide pillars whose lit part rises to each sample (TrendBars -
// the trend under a dial). Same ramp, same unlit tone, same glow as the ring.

const ease: CSSProperties = {
  transitionProperty: "background-color, height, opacity",
  transitionDuration: "var(--rp-dur)",
  transitionTimingFunction: "var(--rp-ease)",
};

/** A horizontal segmented bar: `count` rounded dashes, lit up to `value`. */
export const SegmentBar: FC<{
  value: number | null | undefined;
  min?: number;
  max: number;
  zones?: readonly GaugeZone[];
  count?: number;
  peak?: number | null;
  /** "position" (default): each lit dash takes the colour of its place on the scale, like the dial.
   * "value": every lit dash takes the colour of the value - for a bar that is not a load but a
   * remainder (days left), where the whole bar should read green, amber or red together. */
  tint?: "position" | "value";
  className?: string;
}> = ({ value, min = 0, max, zones = LOAD_ZONES, count = 28, peak, tint = "position", className }) => {
  const known = typeof value === "number" && Number.isFinite(value);
  const f = known ? fractionOf(value as number, min, max) : 0;
  const lit = litSegments(f, count);
  const peakAt = peak != null && Number.isFinite(peak) ? fractionOf(peak, min, max) : null;
  return (
    <div className={classNames("relative flex h-2 w-full gap-[2px]", className)} dir="ltr" aria-hidden="true">
      {Array.from({ length: count }, (_, i) => {
        const on = i < lit;
        const color = smoothColorAt(zones, tint === "value" ? f : (i + 0.5) / count);
        return (
          <span
            key={i}
            className="h-full flex-1 rounded-full"
            style={{
              ...ease,
              background: on ? color : UNLIT,
              boxShadow: on && i === lit - 1 ? glowShadow(color) : undefined,
              transitionDelay: on ? `${Math.min(i, 30) * 10}ms` : undefined,
            }}
          />
        );
      })}
      {peakAt !== null && (
        <span
          className="absolute -inset-y-[3px] w-[2px] rounded-full bg-rapido-text"
          style={{ left: `calc(${(peakAt * 100).toFixed(1)}% - 1px)` }}
        />
      )}
    </div>
  );
};

/** The trend under a dial: one pillar per sample, lit to its value and coloured
 * by the same ramp; the newest is brighter and carries the glow. */
export const TrendBars: FC<{
  values: ReadonlyArray<number | null | undefined>;
  /** The value a full-height pillar stands for; defaults to the largest sample. */
  max?: number;
  min?: number;
  zones?: readonly GaugeZone[];
  /** How many of the newest samples to draw. */
  bars?: number;
  height?: number;
  ariaLabel?: string;
  className?: string;
}> = ({ values, max, min = 0, zones = LOAD_ZONES, bars = 30, height = 22, ariaLabel, className }) => {
  const shown = values.slice(-bars);
  const known = shown.filter((v): v is number => typeof v === "number" && Number.isFinite(v));
  const top = max ?? Math.max(1, ...known);
  // Pad on the left so a young history grows in from the end rather than
  // stretching 3 samples across the whole width.
  const pad = Math.max(0, bars - shown.length);
  const lastIdx = shown.length - 1;
  return (
    <div
      role="img"
      aria-label={ariaLabel ?? (known.length ? String(known[known.length - 1]) : "no data")}
      dir="ltr"
      className={classNames("flex w-full items-end gap-[2px]", className)}
      style={{ height }}
    >
      {Array.from({ length: pad }, (_, i) => (
        <span key={`p${i}`} className="h-full flex-1 rounded-full" style={{ background: UNLIT, opacity: 0.45 }} />
      ))}
      {shown.map((v, i) => {
        const ok = typeof v === "number" && Number.isFinite(v);
        const f = ok ? fractionOf(v as number, min, top) : 0;
        const color = smoothColorAt(zones, f);
        const newest = i === lastIdx;
        return (
          <span key={i} className="relative h-full flex-1 overflow-hidden rounded-full" style={{ background: UNLIT, opacity: newest ? 1 : 0.9 }}>
            {ok && (
              <span
                className="absolute inset-x-0 bottom-0 rounded-full"
                style={{
                  ...ease,
                  height: `${Math.max(14, f * 100).toFixed(1)}%`,
                  background: color,
                  opacity: newest ? 1 : 0.62,
                  boxShadow: newest ? glowShadow(color) : undefined,
                }}
              />
            )}
          </span>
        );
      })}
    </div>
  );
};

/** A labelled stat in the language's type scale: quiet label, big tabular numeral, small unit. */
export const Readout: FC<{ label: string; value: string; unit?: string; className?: string }> = ({
  label,
  value,
  unit,
  className,
}) => (
  <div className={classNames("flex flex-col gap-1", className)}>
    <span className={TYPE.caption}>{label}</span>
    <span className="flex items-baseline gap-1">
      <span className={classNames(TYPE.readout, "text-lg")}>{value}</span>
      {unit && <span className={classNames(TYPE.unit, "text-[11px]")}>{unit}</span>}
    </span>
  </div>
);

/**
 * A segmented bar split between two or more shares - how much of the busiest
 * item's total this one carries (`fill`, 0..100), and who it is made of (up /
 * down). The dash language again: lit dashes take the colour of their share.
 */
export const SplitBar: FC<{
  /** How much of the bar is lit, as a percentage of the whole. */
  fill: number;
  parts: { value: number; color: string }[];
  count?: number;
  className?: string;
}> = ({ fill, parts, count = 32, className }) => {
  const lit = litSegments(Math.min(100, Math.max(0, fill)) / 100, count);
  const total = parts.reduce((a, p) => a + Math.max(0, p.value), 0) || 1;
  const colorAt = (i: number) => {
    let acc = 0;
    const f = (i + 0.5) / Math.max(1, lit);
    for (const p of parts) {
      acc += Math.max(0, p.value) / total;
      if (f <= acc + 1e-9) return p.color;
    }
    return parts[parts.length - 1]?.color ?? UNLIT;
  };
  return (
    <div className={classNames("flex h-2 w-full gap-[2px]", className)} dir="ltr" aria-hidden="true">
      {Array.from({ length: count }, (_, i) => (
        <span
          key={i}
          className="h-full flex-1 rounded-full"
          style={{ ...ease, background: i < lit ? colorAt(i) : UNLIT, transitionDelay: i < lit ? `${Math.min(i, 30) * 10}ms` : undefined }}
        />
      ))}
    </div>
  );
};
