import { FC } from "react";
import classNames from "classnames";
import { Gauge, GaugeProps, gaugePx } from "./Gauge";
import { LOAD_ZONES, TYPE, fractionOf } from "./instrument";
import { SegmentBar, TrendBars } from "./Segments";
import { useAppearance } from "./appearance";

export type ResourceMeterProps = Omit<GaugeProps, "dial"> & {
  /** Force one rendering regardless of the Appearance "gauges or bars" switch. */
  mode?: "gauge" | "bar";
  /** Recent samples of this reading, drawn as a trend right under the meter. */
  trend?: ReadonlyArray<number | null | undefined>;
  /** What a full-height trend pillar stands for; defaults to the meter's own max. */
  trendMax?: number;
};

/**
 * A resource reading (CPU, a quota, a bitrate) drawn as a dial or, when the
 * user prefers classic bars in Appearance, as a segmented bar. Every screen
 * that shows a percentage or a "used of limit" goes through this, so the global
 * switch reaches all of them at once. Both are the same instrument language
 * (see instrument.ts).
 */
export const ResourceMeter: FC<ResourceMeterProps> = ({ mode, trend, trendMax, ...gauge }) => {
  const meters = useAppearance((s) => s.meters);
  const asBar = (mode ?? (meters === "bars" ? "bar" : "gauge")) === "bar";
  const trendEl =
    trend && trend.length > 0 ? (
      <TrendBars
        values={trend}
        max={trendMax ?? gauge.max}
        zones={gauge.zones ?? LOAD_ZONES}
        className="mt-1"
        height={asBar ? 16 : 20}
      />
    ) : null;

  if (asBar)
    return (
      <div className={classNames("flex w-full min-w-0 flex-col", gauge.className)}>
        <BarMeter {...gauge} className={undefined} />
        {trendEl}
      </div>
    );
  if (!trendEl) return <Gauge {...gauge} />;
  return (
    <div className="flex flex-col items-center" style={{ width: gaugePx(gauge.size ?? "md") }}>
      <Gauge {...gauge} />
      <div className="w-[88%]">{trendEl}</div>
    </div>
  );
};

const BarMeter: FC<GaugeProps> = ({
  value,
  min = 0,
  max,
  unit,
  label,
  ariaLabel,
  sublabel,
  zones = LOAD_ZONES,
  peak,
  decimals = 0,
  formatValue,
  loading,
  className,
}) => {
  const known = !loading && typeof value === "number" && Number.isFinite(value);
  const text = known ? (formatValue ? formatValue(value as number) : (value as number).toFixed(decimals)) : "—";
  const unitText = known ? (typeof unit === "function" ? unit(value as number) : unit) : undefined;
  const name = ariaLabel ?? (typeof label === "string" ? label : undefined);

  return (
    <div
      role="meter"
      aria-label={name}
      aria-valuemin={min}
      aria-valuemax={max}
      aria-valuenow={known ? (value as number) : undefined}
      aria-valuetext={known ? `${text}${unitText ? ` ${unitText}` : ""}` : undefined}
      data-fraction={known ? fractionOf(value as number, min, max).toFixed(4) : undefined}
      className={classNames("w-full min-w-0", className)}
    >
      <div className="flex items-baseline justify-between gap-2">
        <span className={classNames(TYPE.caption, "truncate")}>{label}</span>
        <span className="shrink-0" dir="ltr">
          <span className={classNames(TYPE.readout, "text-sm")}>{text}</span>
          {unitText ? <span className={classNames(TYPE.unit, "ms-0.5 text-[11px]")}>{unitText}</span> : null}
        </span>
      </div>
      <SegmentBar value={known ? (value as number) : null} min={min} max={max} zones={zones} peak={peak} className="mt-1.5" />
      {sublabel && <div className={classNames(TYPE.caption, "mt-1")}>{sublabel}</div>}
    </div>
  );
};
