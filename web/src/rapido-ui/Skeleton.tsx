import { FC, HTMLAttributes } from "react";
import classNames from "classnames";

/** A placeholder block with a light sweep across it while data loads. */
export const Skeleton: FC<HTMLAttributes<HTMLDivElement>> = ({ className, ...rest }) => (
  <div
    aria-hidden="true"
    className={classNames("relative overflow-hidden rounded-lg bg-rapido-raised/70", className)}
    {...rest}
  >
    <div
      className="absolute inset-0 animate-rp-shimmer"
      style={{ background: "linear-gradient(90deg, transparent, rgb(var(--rp-shimmer)), transparent)" }}
    />
  </div>
);
