import { FC, useEffect, useState } from "react";
import { ARC, CENTER as C, GaugeZone, MOTION, UNLIT, glow, smoothColorAt } from "./instrument";
import { prefersReducedMotion } from "./motion";

// One ring of the instrument language: small rounded dashes round an open arc,
// lit up to a value in the blended ramp, with a brighter head on the last lit
// dash. The single dial uses one of these, the dual ring uses two.

export const point = (r: number, deg: number): [number, number] => {
  const a = (deg * Math.PI) / 180;
  return [C + r * Math.cos(a), C + r * Math.sin(a)];
};
export const toDeg = (f: number) => ARC.start + ARC.sweep * f;
const n2 = (n: number) => n.toFixed(2);

/** SVG path for the arc between two range fractions. */
export const arcPath = (r: number, f0: number, f1: number): string => {
  const [x0, y0] = point(r, toDeg(f0));
  const [x1, y1] = point(r, toDeg(f1));
  const large = (f1 - f0) * ARC.sweep > 180 ? 1 : 0;
  return `M ${n2(x0)} ${n2(y0)} A ${r} ${r} 0 ${large} 1 ${n2(x1)} ${n2(y1)}`;
};

export const rotateOf = (frac: number) => `rotate(${(frac - 0.5) * ARC.sweep}deg)`;

/** The fraction a ring is currently drawn at. It starts parked and sweeps to
 * `target` once mounted, then follows each change by CSS transition - one
 * state change per reading, nothing per frame. */
export const useSweep = (target: number, reduced: boolean): number => {
  const [shown, setShown] = useState(() => (prefersReducedMotion() ? target : 0));
  useEffect(() => {
    if (reduced) {
      setShown(target);
      return;
    }
    const raf = requestAnimationFrame(() => setShown(target));
    return () => cancelAnimationFrame(raf);
  }, [target, reduced]);
  return shown;
};

export const DashRing: FC<{
  uid: string;
  rIn: number;
  rOut: number;
  count: number;
  /** Dash thickness in viewBox units. */
  dashW: number;
  zones: readonly GaugeZone[];
  /** Fraction currently drawn (animated). */
  shown: number;
  /** The value's colour, for the glow and the head. */
  color: string;
  reduced: boolean;
  /** Draw the bright head on the last lit dash. */
  head?: boolean;
  /** Skip the glow filter (small dials). */
  flat?: boolean;
  testId?: string;
}> = ({ uid, rIn, rOut, count, dashW, zones, shown, color, reduced, head = true, flat = false, testId }) => {
  const mid = (rIn + rOut) / 2;
  const half = (rOut - rIn) / 2 - dashW / 2; // round caps add dashW/2 at each end
  const dashAt = (i: number, a: number, b: number) => {
    const deg = toDeg((i + 0.5) / count);
    const [x0, y0] = point(a, deg);
    const [x1, y1] = point(b, deg);
    return `M${n2(x0)} ${n2(y0)}L${n2(x1)} ${n2(y1)}`;
  };
  const dashes = Array.from({ length: count }, (_, i) => ({
    d: dashAt(i, mid - half, mid + half),
    color: smoothColorAt(zones, (i + 0.5) / count),
    i,
  }));
  const sweepT = reduced ? "none" : `stroke-dasharray ${MOTION.ms}ms ${MOTION.ease}`;
  const moveT = reduced ? "none" : `transform ${MOTION.ms}ms ${MOTION.ease}`;
  const maskId = `lit${uid}`;

  return (
    <g>
      <defs>
        <mask id={maskId} maskUnits="userSpaceOnUse" x="0" y="0" width="200" height="200">
          <path
            d={arcPath(mid, 0, 1)}
            pathLength={100}
            fill="none"
            stroke="#fff"
            strokeWidth={rOut - rIn + 12}
            strokeDasharray={`${(shown * 100).toFixed(3)} 200`}
            style={{ transition: sweepT }}
          />
        </mask>
      </defs>

      <path
        d={dashes.map((d) => d.d).join("")}
        fill="none"
        strokeLinecap="round"
        strokeWidth={dashW}
        style={{ stroke: UNLIT }}
      />
      <g style={flat ? undefined : { filter: glow(color) }}>
        <g mask={`url(#${maskId})`} fill="none" strokeLinecap="round" strokeWidth={dashW}>
          {dashes.map((d) => (
            <path key={d.i} d={d.d} style={{ stroke: d.color }} />
          ))}
        </g>
      </g>
      {head && shown > 0 && (
        <g
          data-testid={testId}
          style={{
            transform: rotateOf(Math.max(0, shown - 0.5 / count)),
            transformOrigin: `${C}px ${C}px`,
            transition: moveT,
            filter: flat ? undefined : glow(color),
          }}
        >
          <line
            x1={C}
            y1={C - (rIn - 2.5)}
            x2={C}
            y2={C - (rOut + 2.5)}
            strokeLinecap="round"
            strokeWidth={dashW * 0.9}
            style={{ stroke: "rgb(var(--rp-text))" }}
          />
        </g>
      )}
    </g>
  );
};

/** The flat plate every dial sits on: an inset well with a hairline rim and a
 * lit top edge. Must be rendered inside an <svg viewBox="0 0 200 200">. */
export const Plate: FC<{ uid: string }> = ({ uid }) => (
  <g>
    <defs>
      <radialGradient id={`plate${uid}`} cx="50%" cy="30%" r="80%">
        <stop offset="0" style={{ stopColor: "rgb(var(--rp-raised))" }} />
        <stop offset="0.7" style={{ stopColor: "rgb(var(--rp-inset))" }} />
        <stop offset="1" style={{ stopColor: "rgb(var(--rp-inset))" }} />
      </radialGradient>
      <linearGradient id={`edge${uid}`} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" style={{ stopColor: "rgb(var(--rp-hl))", stopOpacity: 0.16 }} />
        <stop offset="0.5" style={{ stopColor: "rgb(var(--rp-hl))", stopOpacity: 0.02 }} />
        <stop offset="1" style={{ stopColor: "rgb(var(--rp-sh))", stopOpacity: 0.35 }} />
      </linearGradient>
    </defs>
    <circle cx={C} cy={C} r={97} fill={`url(#plate${uid})`} />
    <circle cx={C} cy={C} r={97} fill="none" stroke={`url(#edge${uid})`} strokeWidth={1.4} />
  </g>
);
