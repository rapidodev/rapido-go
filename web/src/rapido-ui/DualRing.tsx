import { FC, useId } from "react";
import classNames from "classnames";
import { formatBitrate, isNumber } from "utils/tunnelMetrics";
import { DashRing, Plate, useSweep } from "./DashRing";
import { Gauge } from "./Gauge";
import {
  GAUGE_CYAN,
  GAUGE_VIOLET,
  TYPE,
  fractionOf,
  litSegments,
  saturatingZones,
  smoothColorAt,
  solidZones,
} from "./instrument";
import { useAppearance } from "./appearance";
import { useReducedMotion } from "./motion";

/** The two stream colours: the accent for what comes in, violet for what goes out. */
export const RX_COLOR = GAUGE_CYAN;
export const TX_COLOR = GAUGE_VIOLET;

const OUTER = { rIn: 72, rOut: 90, count: 48 };
const INNER = { rIn: 47, rOut: 65, count: 38 };

/**
 * Two concentric rings of the instrument language on one plate: the outer ring
 * is what comes in (download), the inner what goes out (upload), and the
 * middle reads the two together. Both rings share one scale, so they compare
 * honestly. With the needle dial chosen in Appearance it becomes two slim
 * needle dials side by side instead.
 */
export const DualRing: FC<{
  rx: number | null | undefined;
  tx: number | null | undefined;
  /** Full scale for both rings, in bits per second. */
  max: number;
  /** Colour the rings by saturation (a known link speed) rather than a flat hue. */
  saturating?: boolean;
  /** Accessible name for the whole instrument. */
  ariaLabel: string;
  rxLabel: string;
  txLabel: string;
  size?: number;
  className?: string;
}> = ({ rx, tx, max, saturating = false, ariaLabel, rxLabel, txLabel, size = 184, className }) => {
  const uid = useId().replace(/[^a-zA-Z0-9]/g, "");
  const reduced = useReducedMotion();
  const dial = useAppearance((s) => s.dial);
  const rxKnown = isNumber(rx);
  const txKnown = isNumber(tx);
  const rxF = rxKnown ? fractionOf(rx as number, 0, max) : 0;
  const txF = txKnown ? fractionOf(tx as number, 0, max) : 0;
  const rxLit = litSegments(rxF, OUTER.count) / OUTER.count;
  const txLit = litSegments(txF, INNER.count) / INNER.count;
  const rxShown = useSweep(rxLit, reduced);
  const txShown = useSweep(txLit, reduced);
  const rxZones = saturating ? saturatingZones(RX_COLOR) : solidZones(RX_COLOR);
  const txZones = saturating ? saturatingZones(TX_COLOR) : solidZones(TX_COLOR);
  const total = (rxKnown ? (rx as number) : 0) + (txKnown ? (tx as number) : 0);
  const anyKnown = rxKnown || txKnown;
  const { value, unit } = formatBitrate(total);

  if (dial === "needle") {
    const half = Math.round(size * 0.58);
    return (
      <div className={classNames("flex items-start justify-center gap-1", className)} role="group" aria-label={ariaLabel}>
        <Gauge value={rxKnown ? (rx as number) : null} max={max} size={half} dial="needle" zones={rxZones}
          formatValue={(v) => formatBitrate(v).value} unit={(v) => formatBitrate(v).unit} ariaLabel={rxLabel} label={`↓ ${rxLabel}`} />
        <Gauge value={txKnown ? (tx as number) : null} max={max} size={half} dial="needle" zones={txZones}
          formatValue={(v) => formatBitrate(v).value} unit={(v) => formatBitrate(v).unit} ariaLabel={txLabel} label={`↑ ${txLabel}`} />
      </div>
    );
  }

  return (
    <div
      role="img"
      aria-label={ariaLabel}
      data-state={anyKnown ? "ready" : "unknown"}
      data-rx={rxF.toFixed(4)}
      data-tx={txF.toFixed(4)}
      dir="ltr"
      className={classNames("relative", className)}
      style={{ width: size, height: size }}
    >
      <svg viewBox="0 0 200 200" width="100%" height="100%" aria-hidden="true" focusable="false" className="overflow-visible">
        <Plate uid={uid} />
        <DashRing uid={`${uid}o`} {...OUTER} dashW={4} zones={rxZones} shown={rxShown} reduced={reduced}
          color={smoothColorAt(rxZones, rxF)} head={rxKnown} testId="dual-rx-head" />
        <DashRing uid={`${uid}i`} {...INNER} dashW={3.6} zones={txZones} shown={txShown} reduced={reduced}
          color={smoothColorAt(txZones, txF)} head={txKnown} testId="dual-tx-head" />
      </svg>
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-1">
        <span className={TYPE.readout} style={{ fontSize: size * (value.length > 4 ? 0.15 : 0.18) }} data-testid="dual-value">
          {anyKnown ? value : "—"}
        </span>
        {anyKnown && (
          <span className={TYPE.unit} style={{ fontSize: Math.max(9, size * 0.06) }}>
            {unit}
          </span>
        )}
      </div>
    </div>
  );
};
