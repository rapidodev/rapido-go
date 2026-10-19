import { FC, HTMLAttributes, PropsWithChildren } from "react";
import classNames from "classnames";
import { Badge, BadgeTone, PulseDot } from "./Badge";

/**
 * A status label with its lit dot: "Up", "Degraded", "Down". The dot pulses
 * while `live` (the state is current and being watched). One component so
 * every status in the panel - tunnels, nodes, checks, links - reads the same.
 */
export const StatusPill: FC<
  PropsWithChildren<HTMLAttributes<HTMLSpanElement> & { tone?: BadgeTone; live?: boolean }>
> = ({ tone = "gray", live = false, className, children, ...rest }) => (
  <Badge tone={tone} className={classNames("gap-1.5 whitespace-nowrap", className)} {...rest}>
    <PulseDot tone={tone} live={live} />
    {children}
  </Badge>
);
