import { FC, useId } from "react";
import classNames from "classnames";

// A small SVG line/area chart with threshold colouring and no dependencies:
// the line (and the soft fill under it) changes colour where the value crosses
// a threshold - green below, amber past one limit, red past the next - which
// is what a latency or loss series needs to read at a glance. `Sparkline` is
// the same thing without axes, for tight cards.

export type ChartBand = {
  /** The value at which this colour starts. Bands are applied in ascending order. */
  from: number;
  color: string;
  label?: string;
};

export type AreaChartProps = {
  /** null / NaN entries are gaps: the line breaks there instead of dropping to zero. */
  values: ReadonlyArray<number | null | undefined>;
  /** Colour below the first band (or everywhere, with no bands). */
  color?: string;
  bands?: readonly ChartBand[];
  /** Fixed value range; defaults to the data's own (min is clamped to 0 when all values are non-negative). */
  min?: number;
  max?: number;
  height?: number;
  /** Fill under the line. */
  area?: boolean;
  /** Draw the threshold lines and the min/max labels. */
  detail?: boolean;
  formatValue?: (v: number) => string;
  ariaLabel?: string;
  className?: string;
};

const W = 300;
const PAD_X = 2;
const PAD_Y = 4;

const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

/** Splits a series at its gaps into runs of [x, value] pairs. */
export const chartRuns = (values: AreaChartProps["values"]): { i: number; v: number }[][] => {
  const runs: { i: number; v: number }[][] = [];
  let cur: { i: number; v: number }[] = [];
  values.forEach((v, i) => {
    if (finite(v)) cur.push({ i, v });
    else if (cur.length) {
      runs.push(cur);
      cur = [];
    }
  });
  if (cur.length) runs.push(cur);
  return runs;
};

export const chartDomain = (
  values: AreaChartProps["values"],
  min?: number,
  max?: number
): { lo: number; hi: number } => {
  const nums = values.filter(finite);
  let lo = min ?? (nums.length ? Math.min(...nums) : 0);
  let hi = max ?? (nums.length ? Math.max(...nums) : 1);
  // A series of non-negative readings sits on a zero baseline unless told otherwise.
  if (min === undefined && nums.length > 0 && nums.every((n) => n >= 0)) lo = 0;
  if (hi <= lo) hi = lo + 1;
  return { lo, hi };
};

export const AreaChart: FC<AreaChartProps> = ({
  values,
  color = "rgb(var(--rp-accent))",
  bands = [],
  min,
  max,
  height = 96,
  area = true,
  detail = true,
  formatValue = (v) => String(parseFloat(v.toFixed(1))),
  ariaLabel,
  className,
}) => {
  const uid = useId().replace(/[^a-zA-Z0-9]/g, "");
  const { lo, hi } = chartDomain(values, min, max);
  const H = height;
  const n = values.length;
  const x = (i: number) => (n <= 1 ? W / 2 : PAD_X + (i / (n - 1)) * (W - 2 * PAD_X));
  const y = (v: number) => PAD_Y + (1 - (Math.min(hi, Math.max(lo, v)) - lo) / (hi - lo)) * (H - 2 * PAD_Y);
  const runs = chartRuns(values);
  const known = values.filter(finite);

  const line = (run: { i: number; v: number }[]) =>
    run.map((p, k) => `${k === 0 ? "M" : "L"}${x(p.i).toFixed(2)} ${y(p.v).toFixed(2)}`).join(" ");
  const fill = (run: { i: number; v: number }[]) =>
    `${line(run)} L${x(run[run.length - 1].i).toFixed(2)} ${H - PAD_Y} L${x(run[0].i).toFixed(2)} ${H - PAD_Y} Z`;

  // Vertical gradient with hard stops at each threshold. Offsets run top to
  // bottom, so a higher value is nearer offset 0.
  const sorted = [...bands].sort((a, b) => a.from - b.from);
  const stops: { offset: number; color: string }[] = [];
  const offsetOf = (v: number) => (y(v) - 0) / H;
  if (sorted.length === 0) stops.push({ offset: 0, color }, { offset: 1, color });
  else {
    // top -> bottom: highest band first
    const rev = [...sorted].reverse();
    rev.forEach((b, k) => {
      const top = k === 0 ? 0 : offsetOf(rev[k - 1].from);
      const bottom = offsetOf(b.from);
      stops.push({ offset: top, color: b.color }, { offset: Math.min(1, Math.max(top, bottom)), color: b.color });
    });
    const base = offsetOf(sorted[0].from);
    stops.push({ offset: Math.min(1, Math.max(0, base)), color }, { offset: 1, color });
  }

  const last = known.length ? known[known.length - 1] : null;
  const label =
    ariaLabel ??
    (known.length
      ? `${formatValue(last as number)} (min ${formatValue(Math.min(...known))}, max ${formatValue(Math.max(...known))})`
      : "no data");

  return (
    <div role="img" aria-label={label} className={classNames("w-full", className)}>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        width="100%"
        height={H}
        className="block overflow-visible"
        aria-hidden="true"
        focusable="false"
      >
        <defs>
          <linearGradient id={`ln${uid}`} gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="0" y2={H}>
            {stops.map((s, k) => (
              <stop key={k} offset={s.offset} style={{ stopColor: s.color }} />
            ))}
          </linearGradient>
          <linearGradient id={`fl${uid}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="#fff" stopOpacity="0.28" />
            <stop offset="1" stopColor="#fff" stopOpacity="0" />
          </linearGradient>
          <mask id={`mk${uid}`} maskUnits="userSpaceOnUse" x="0" y="0" width={W} height={H}>
            <rect width={W} height={H} fill={`url(#fl${uid})`} />
          </mask>
        </defs>

        {detail &&
          sorted.map((b) => (
            <line
              key={b.from}
              x1={0}
              x2={W}
              y1={y(b.from)}
              y2={y(b.from)}
              strokeDasharray="3 4"
              strokeWidth={1}
              vectorEffect="non-scaling-stroke"
              style={{ stroke: b.color }}
              opacity={0.55}
            />
          ))}

        {area && (
          <g mask={`url(#mk${uid})`}>
            {runs
              .filter((r) => r.length > 1)
              .map((r, k) => (
                <path key={k} d={fill(r)} style={{ fill: `url(#ln${uid})` }} />
              ))}
          </g>
        )}
        {runs.map((r, k) =>
          r.length > 1 ? (
            <path
              key={k}
              d={line(r)}
              fill="none"
              strokeWidth={2}
              strokeLinejoin="round"
              strokeLinecap="round"
              vectorEffect="non-scaling-stroke"
              style={{ stroke: `url(#ln${uid})` }}
            />
          ) : (
            <circle key={k} cx={x(r[0].i)} cy={y(r[0].v)} r={2.5} style={{ fill: color }} />
          )
        )}
      </svg>
      {detail && known.length > 0 && (
        <div className="mt-1 flex justify-between text-[11px] tabular-nums text-rapido-muted" dir="ltr">
          <span>{formatValue(lo)}</span>
          <span>{formatValue(hi)}</span>
        </div>
      )}
    </div>
  );
};

/** The same chart with no thresholds lines or labels, sized for a card corner. */
export const Sparkline: FC<Omit<AreaChartProps, "detail" | "height"> & { height?: number }> = ({
  height = 32,
  ...rest
}) => <AreaChart {...rest} height={height} detail={false} />;
