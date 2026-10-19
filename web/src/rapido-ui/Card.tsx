import { FC, HTMLAttributes, PropsWithChildren, PointerEvent } from "react";
import classNames from "classnames";
import { useTilt } from "./useTilt";

export type CardProps = PropsWithChildren<
  HTMLAttributes<HTMLDivElement> & {
    /** Shadow depth: 1 = resting on the page, 2 = default, 3 = floating (modals). */
    elevation?: 1 | 2 | 3;
    /** Tip toward the pointer with a little perspective, and light a glare under it. Off by default: it suits tiles and small cards, not forms or tables. */
    tilt?: boolean;
    /** Largest tilt in degrees. */
    tiltMax?: number;
  }
>;

const elevationClass = { 1: "shadow-rp-1", 2: "shadow-rp-2", 3: "shadow-rp-3" } as const;

// A bevelled glass panel (see .rp-card in tailwind.css): translucent surface
// colour under a diagonal sheen, a lit top edge and layered shadows come from
// `.rp-card` + `shadow-rp-*`. The shared tilt logic is useTilt, bound here so
// every tilting surface behaves identically.
export const Card: FC<CardProps> = ({
  className,
  children,
  elevation = 2,
  tilt = false,
  tiltMax = 4,
  onPointerMove,
  onPointerLeave,
  ...rest
}) => {
  const tiltBindings = useTilt<HTMLDivElement>({ max: tiltMax, enabled: tilt });

  return (
    <div
      ref={tilt ? tiltBindings.ref : undefined}
      className={classNames(
        "rp-card min-w-0 max-w-full rounded-xl2 border border-rapido-border bg-rapido-surface/75 p-5",
        elevationClass[elevation],
        tilt && "rp-tilt rp-interactive",
        className
      )}
      onPointerMove={
        tilt
          ? (e: PointerEvent<HTMLDivElement>) => {
              tiltBindings.onPointerMove(e);
              onPointerMove?.(e);
            }
          : onPointerMove
      }
      onPointerLeave={
        tilt
          ? (e: PointerEvent<HTMLDivElement>) => {
              tiltBindings.onPointerLeave();
              onPointerLeave?.(e);
            }
          : onPointerLeave
      }
      {...rest}
    >
      {children}
    </div>
  );
};

export const CardTitle: FC<PropsWithChildren<HTMLAttributes<HTMLDivElement>>> = ({
  className,
  children,
  ...rest
}) => (
  <div
    className={classNames("text-sm font-semibold text-rapido-text", className)}
    {...rest}
  >
    {children}
  </div>
);

export const CardSubtitle: FC<PropsWithChildren<HTMLAttributes<HTMLDivElement>>> = ({
  className,
  children,
  ...rest
}) => (
  <div className={classNames("text-xs text-rapido-muted", className)} {...rest}>
    {children}
  </div>
);
