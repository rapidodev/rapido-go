import { FC, ReactNode } from "react";
import classNames from "classnames";
import { BadgeTone, PulseDot } from "./Badge";

export type TimelineItem = {
  id: string | number;
  /** When it happened - already formatted (and direction-isolated) by the caller. */
  time: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  tone?: BadgeTone;
  /** Still going on: the marker pulses. */
  live?: boolean;
  /** Extra content under the text (chips, a duration, an action). */
  meta?: ReactNode;
};

const markerRing: Record<BadgeTone, string> = {
  green: "border-emerald-400/70 bg-emerald-400/20",
  sky: "border-sky-400/70 bg-sky-400/20",
  yellow: "border-yellow-400/70 bg-yellow-400/20",
  orange: "border-orange-400/70 bg-orange-400/20",
  red: "border-red-400/70 bg-red-400/20",
  brand: "border-rapido-accent/70 bg-rapido-accent/20",
  violet: "border-rapido-violet/70 bg-rapido-violet/20",
  gray: "border-rapido-muted/60 bg-rapido-muted/15",
};

/**
 * A vertical event timeline - outages, deploys, failovers. Newest-first or
 * oldest-first is the caller's order. The rail and markers use logical
 * properties (start / border-s), so it mirrors correctly in RTL.
 */
export const Timeline: FC<{
  items: readonly TimelineItem[];
  /** Shown instead of the list when there are no items. */
  empty?: ReactNode;
  className?: string;
  "aria-label"?: string;
}> = ({ items, empty, className, "aria-label": ariaLabel }) => {
  if (items.length === 0) return <>{empty ?? null}</>;
  return (
    <ol aria-label={ariaLabel} className={classNames("relative flex flex-col", className)}>
      {items.map((item, i) => {
        const tone = item.tone ?? "gray";
        const last = i === items.length - 1;
        return (
          <li key={item.id} className={classNames("relative ps-8", last ? "pb-0" : "pb-5")}>
            {/* the rail between this marker and the next */}
            {!last && <span aria-hidden="true" className="absolute bottom-0 start-[0.55rem] top-4 w-px bg-rapido-border" />}
            <span
              aria-hidden="true"
              className={classNames(
                "absolute start-0 top-1 flex h-[1.15rem] w-[1.15rem] items-center justify-center rounded-full border",
                markerRing[tone]
              )}
            >
              <PulseDot tone={tone} live={!!item.live} />
            </span>
            <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
              <div className="text-sm font-medium text-rapido-text">{item.title}</div>
              <div className="text-xs tabular-nums text-rapido-muted">{item.time}</div>
            </div>
            {item.description && <div className="mt-0.5 text-xs text-rapido-muted">{item.description}</div>}
            {item.meta && <div className="mt-1.5 flex flex-wrap items-center gap-1.5">{item.meta}</div>}
          </li>
        );
      })}
    </ol>
  );
};
