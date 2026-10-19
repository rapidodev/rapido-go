import { FC, ReactNode, useId } from "react";
import { numberWithCommas } from "utils/formatByte";
import { CountUp } from "./CountUp";
import { Plate, point, toDeg } from "./DashRing";
import { TYPE, UNLIT } from "./instrument";

// The instrument ring used as a distribution: the same open ring of rounded
// dashes, each dash coloured by the share it belongs to, with the total in the
// middle. It is how "how many users are in each status" is drawn - a gauge
// that is full by definition, split into parts.

export type Slice = { key: string; value: number; color: string };

/** Splits `count` dashes between the slices by their share (largest remainder),
 * so the dashes always add up to exactly `count` and a non-zero share always gets one. */
export const allocateDashes = (values: number[], count: number): number[] => {
  const total = values.reduce((a, v) => a + Math.max(0, v), 0);
  if (total <= 0) return values.map(() => 0);
  const raw = values.map((v) => (Math.max(0, v) / total) * count);
  const out = raw.map((r, i) => (values[i] > 0 ? Math.max(1, Math.floor(r)) : 0));
  let diff = count - out.reduce((a, b) => a + b, 0);
  const order = raw.map((r, i) => ({ i, rem: r - Math.floor(r) })).sort((a, b) => b.rem - a.rem);
  let guard = 0;
  while (diff !== 0 && guard++ < 1000) {
    const pick = order[guard % order.length];
    if (diff > 0 && values[pick.i] > 0) {
      out[pick.i]++;
      diff--;
    } else if (diff < 0 && out[pick.i] > 1) {
      out[pick.i]--;
      diff++;
    }
  }
  return out;
};

export const DistributionRing: FC<{
  slices: Slice[];
  size?: number;
  caption?: ReactNode;
  ariaLabel: string;
}> = ({ slices, size = 160, caption, ariaLabel }) => {
  const uid = useId().replace(/[^a-zA-Z0-9]/g, "");
  const N = 56;
  const total = slices.reduce((a, s) => a + Math.max(0, s.value), 0);
  const counts = allocateDashes(slices.map((s) => s.value), N);
  const dashes: { d: string; color: string }[] = [];
  let i = 0;
  slices.forEach((s, k) => {
    for (let c = 0; c < counts[k]; c++, i++) {
      const deg = toDeg((i + 0.5) / N);
      const [x0, y0] = point(72, deg);
      const [x1, y1] = point(88, deg);
      dashes.push({ d: `M${x0.toFixed(2)} ${y0.toFixed(2)}L${x1.toFixed(2)} ${y1.toFixed(2)}`, color: s.color });
    }
  });
  for (; i < N; i++) {
    const deg = toDeg((i + 0.5) / N);
    const [x0, y0] = point(72, deg);
    const [x1, y1] = point(88, deg);
    dashes.push({ d: `M${x0.toFixed(2)} ${y0.toFixed(2)}L${x1.toFixed(2)} ${y1.toFixed(2)}`, color: UNLIT });
  }

  return (
    <div role="img" aria-label={ariaLabel} dir="ltr" className="relative shrink-0" style={{ width: size, height: size }}>
      <svg viewBox="0 0 200 200" width="100%" height="100%" aria-hidden="true" focusable="false" className="overflow-visible">
        <Plate uid={uid} />
        <g fill="none" strokeLinecap="round" strokeWidth={4.4}>
          {dashes.map((d, k) => (
            <path key={k} d={d.d} stroke={d.color} />
          ))}
        </g>
      </svg>
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-1">
        <span className={TYPE.readout} style={{ fontSize: size * 0.2 }}>
          <CountUp value={total} format={(n) => numberWithCommas(Math.round(n)) ?? "0"} />
        </span>
        {caption && (
          <span className={TYPE.unit} style={{ fontSize: Math.max(9, size * 0.06) }}>
            {caption}
          </span>
        )}
      </div>
    </div>
  );
};
