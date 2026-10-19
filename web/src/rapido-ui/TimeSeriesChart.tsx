import { FC, KeyboardEvent, PointerEvent, useId, useMemo, useRef, useState } from "react";
import classNames from "classnames";
import { TYPE, glow } from "./instrument";

// The one time-series chart: a gradient area and a glowing line per series on a
// shared time axis, with a hover / keyboard readout. Series may have different
// timestamps (a node's history and a tunnel's samples); a gap (null) breaks the
// line instead of dropping to zero. No library - the same recipe as the dials.

export type ChartPoint = { t: number; v: number | null };
export type ChartSeries = { key: string; label: string; color: string; points: ChartPoint[] };

const W = 640;
const PAD_T = 8;
const PAD_B = 4;

const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

/** The points of a series at or nearest to time `t`, or null when it has none within `slack` ms. */
export const nearestPoint = (points: ChartPoint[], t: number, slack = Infinity): ChartPoint | null => {
  let best: ChartPoint | null = null;
  let bestD = Infinity;
  for (const p of points) {
    const d = Math.abs(p.t - t);
    if (d < bestD) {
      best = p;
      bestD = d;
    }
  }
  return best && bestD <= slack ? best : null;
};

export const timeDomain = (series: ChartSeries[]): { t0: number; t1: number } | null => {
  let t0 = Infinity;
  let t1 = -Infinity;
  for (const s of series) for (const p of s.points) if (finite(p.v)) {
    t0 = Math.min(t0, p.t);
    t1 = Math.max(t1, p.t);
  }
  return t0 <= t1 ? { t0, t1: t1 === t0 ? t0 + 1 : t1 } : null;
};

export const TimeSeriesChart: FC<{
  series: ChartSeries[];
  /** Format a value for the axis and the readout. */
  format: (v: number) => string;
  /** Format a timestamp for the axis and the readout. */
  formatTime: (t: number) => string;
  height?: number;
  /** Shown instead of the chart when no series has a point. */
  empty?: string;
  ariaLabel: string;
  className?: string;
}> = ({ series, format, formatTime, height = 220, empty, ariaLabel, className }) => {
  const uid = useId().replace(/[^a-zA-Z0-9]/g, "");
  const dom = useMemo(() => timeDomain(series), [series]);
  const [hoverT, setHoverT] = useState<number | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const raf = useRef(0);

  const max = useMemo(() => {
    let m = 0;
    for (const s of series) for (const p of s.points) if (finite(p.v)) m = Math.max(m, p.v);
    return m > 0 ? m * 1.12 : 1;
  }, [series]);

  if (!dom) {
    return (
      <div className={classNames("flex items-center justify-center rounded-lg border border-dashed border-rapido-border text-xs text-rapido-muted", className)} style={{ height }}>
        {empty}
      </div>
    );
  }

  const H = height;
  const x = (t: number) => ((t - dom.t0) / (dom.t1 - dom.t0)) * W;
  const y = (v: number) => PAD_T + (1 - Math.min(max, Math.max(0, v)) / max) * (H - PAD_T - PAD_B);

  const runs = (pts: ChartPoint[]) => {
    const out: ChartPoint[][] = [];
    let cur: ChartPoint[] = [];
    for (const p of pts) {
      if (finite(p.v)) cur.push(p);
      else if (cur.length) {
        out.push(cur);
        cur = [];
      }
    }
    if (cur.length) out.push(cur);
    return out;
  };
  const line = (r: ChartPoint[]) => r.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)} ${y(p.v as number).toFixed(1)}`).join(" ");
  const area = (r: ChartPoint[]) => `${line(r)} L${x(r[r.length - 1].t).toFixed(1)} ${H - PAD_B} L${x(r[0].t).toFixed(1)} ${H - PAD_B} Z`;

  const setFromClientX = (clientX: number) => {
    const el = box.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    if (r.width <= 0) return;
    const f = Math.min(1, Math.max(0, (clientX - r.left) / r.width));
    setHoverT(dom.t0 + f * (dom.t1 - dom.t0));
  };
  const onMove = (e: PointerEvent) => {
    const cx = e.clientX;
    if (raf.current) return;
    raf.current = requestAnimationFrame(() => {
      raf.current = 0;
      setFromClientX(cx);
    });
  };
  const step = (dir: 1 | -1) => {
    const span = dom.t1 - dom.t0;
    setHoverT((cur) => Math.min(dom.t1, Math.max(dom.t0, (cur ?? dom.t1) + (dir * span) / 40)));
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowLeft") {
      e.preventDefault();
      step(-1);
    } else if (e.key === "ArrowRight") {
      e.preventDefault();
      step(1);
    } else if (e.key === "Escape" || e.key === "Home") setHoverT(null);
  };

  const gridY = [0.25, 0.5, 0.75, 1].map((f) => ({ f, v: max * f }));
  const hoverRows =
    hoverT === null
      ? []
      : series
          .map((s) => ({ s, p: nearestPoint(s.points.filter((p) => finite(p.v)), hoverT, (dom.t1 - dom.t0) / 6) }))
          .filter((r) => r.p);
  const hoverPct = hoverT === null ? 0 : ((hoverT - dom.t0) / (dom.t1 - dom.t0)) * 100;

  return (
    <div className={classNames("flex flex-col gap-1.5", className)} dir="ltr">
      <div
        ref={box}
        role="img"
        tabIndex={0}
        aria-label={ariaLabel}
        onPointerMove={onMove}
        onPointerLeave={() => setHoverT(null)}
        onBlur={() => setHoverT(null)}
        onKeyDown={onKey}
        className="relative rounded-lg bg-rapido-inset/60 shadow-rp-inset outline-none focus-visible:ring-2 focus-visible:ring-rapido-accent/50"
        style={{ height: H }}
      >
        <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" width="100%" height={H} className="block overflow-visible" aria-hidden="true" focusable="false">
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`g${uid}${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" style={{ stopColor: s.color, stopOpacity: 0.38 }} />
                <stop offset="1" style={{ stopColor: s.color, stopOpacity: 0 }} />
              </linearGradient>
            ))}
          </defs>
          {gridY.map((g) => (
            <line key={g.f} x1={0} x2={W} y1={y(g.v)} y2={y(g.v)} strokeDasharray="2 5" strokeLinecap="round" vectorEffect="non-scaling-stroke" strokeWidth={1} style={{ stroke: "rgb(var(--rp-unlit))" }} />
          ))}
          {series.map((s) =>
            runs(s.points).map((r, i) =>
              r.length > 1 ? (
                <g key={`${s.key}${i}`}>
                  <path d={area(r)} fill={`url(#g${uid}${s.key})`} />
                  <path d={line(r)} fill="none" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" style={{ stroke: s.color, filter: glow(s.color) }} />
                </g>
              ) : null
            )
          )}
        </svg>
        {/* the y scale, in the type recipe */}
        <div className="pointer-events-none absolute inset-y-0 start-1 flex flex-col justify-between py-1">
          {[...gridY].reverse().map((g) => (
            <span key={g.f} className={TYPE.axis} style={{ opacity: 0.8 }}>
              {format(g.v)}
            </span>
          ))}
        </div>
        {hoverT !== null && (
          <>
            <div className="pointer-events-none absolute inset-y-0 w-px bg-rapido-text/40" style={{ left: `${hoverPct}%` }} />
            {hoverRows.map(({ s, p }) => (
              <span
                key={s.key}
                aria-hidden="true"
                className="pointer-events-none absolute h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border border-rapido-bg"
                style={{ left: `${((p as ChartPoint).t - dom.t0) / (dom.t1 - dom.t0) * 100}%`, top: y((p as ChartPoint).v as number), background: s.color }}
              />
            ))}
            <div
              data-testid="chart-readout"
              className="pointer-events-none absolute top-1 z-10 min-w-[8rem] rounded-lg border border-rapido-border bg-rapido-surface/95 px-2.5 py-1.5 text-[11px] shadow-rp-2"
              style={hoverPct > 60 ? { right: `${100 - hoverPct + 1.5}%` } : { left: `${hoverPct + 1.5}%` }}
            >
              <div className={TYPE.axis}>{formatTime(hoverT)}</div>
              {hoverRows.map(({ s, p }) => (
                <div key={s.key} className="mt-0.5 flex items-center gap-1.5">
                  <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-full" style={{ background: s.color }} />
                  <span className="min-w-0 flex-1 truncate text-rapido-muted">{s.label}</span>
                  <span className="font-semibold tabular-nums text-rapido-text">{format((p as ChartPoint).v as number)}</span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
      <div className="flex justify-between">
        <span className={TYPE.axis}>{formatTime(dom.t0)}</span>
        <span className={TYPE.axis}>{formatTime(dom.t1)}</span>
      </div>
    </div>
  );
};
