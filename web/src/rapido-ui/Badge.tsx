import { FC, HTMLAttributes, PropsWithChildren } from "react";
import classNames from "classnames";

export type BadgeTone =
  | "green"
  | "sky"
  | "yellow"
  | "orange"
  | "red"
  // Named for what it renders, not what it once did - this tone is the
  // brand accent (cyan in the control-room palette), not literally purple.
  | "brand"
  | "violet"
  | "gray";

// Each pill is a tinted fill plus a faint ring of its own colour, so it reads
// as a small lit lozenge rather than a flat rectangle.
const toneClasses: Record<BadgeTone, string> = {
  green: "bg-emerald-500/15 text-emerald-300 ring-emerald-400/25",
  sky: "bg-sky-500/15 text-sky-300 ring-sky-400/25",
  yellow: "bg-yellow-500/15 text-yellow-300 ring-yellow-400/25",
  orange: "bg-orange-500/15 text-orange-300 ring-orange-400/25",
  red: "bg-red-500/15 text-red-300 ring-red-400/25",
  brand: "bg-rapido-accent/15 text-rapido-accent ring-rapido-accent/25",
  violet: "bg-rapido-violet/15 text-rapido-violet ring-rapido-violet/25",
  gray: "bg-rapido-raised text-rapido-muted ring-white/10",
};

export const Badge: FC<
  PropsWithChildren<HTMLAttributes<HTMLSpanElement> & { tone?: BadgeTone }>
> = ({ tone = "gray", className, children, ...rest }) => (
  <span
    className={classNames(
      "inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset",
      toneClasses[tone],
      className
    )}
    {...rest}
  >
    {children}
  </span>
);

const dotClasses: Record<BadgeTone, string> = {
  green: "bg-emerald-400",
  sky: "bg-sky-400",
  yellow: "bg-yellow-400",
  orange: "bg-orange-400",
  red: "bg-red-400",
  brand: "bg-rapido-accent",
  violet: "bg-rapido-violet",
  gray: "bg-rapido-muted",
};

const dotGlow: Record<BadgeTone, string> = {
  green: "shadow-[0_0_8px_1px_rgba(52,211,153,.7)]",
  sky: "shadow-[0_0_8px_1px_rgba(56,189,248,.7)]",
  yellow: "shadow-[0_0_8px_1px_rgba(250,204,21,.7)]",
  orange: "shadow-[0_0_8px_1px_rgba(251,146,60,.7)]",
  red: "shadow-[0_0_8px_1px_rgba(248,113,113,.7)]",
  brand: "shadow-[0_0_8px_1px_rgba(34,211,238,.7)]",
  violet: "shadow-[0_0_8px_1px_rgba(167,139,250,.7)]",
  gray: "",
};

/** A status dot - solid and softly glowing, with an expanding ring behind it
 * while `live`. The ring is what a glance actually catches; a static dot alone
 * reads as decoration, not as "this is happening right now". */
export const PulseDot: FC<{ tone?: BadgeTone; live?: boolean; className?: string }> = ({
  tone = "gray",
  live = true,
  className,
}) => (
  <span className={classNames("relative inline-flex h-2 w-2 shrink-0", className)}>
    {live && (
      <span
        className={classNames(
          "absolute inline-flex h-full w-full animate-ping rounded-full opacity-60",
          dotClasses[tone]
        )}
      />
    )}
    <span
      className={classNames(
        "relative inline-flex h-2 w-2 rounded-full",
        dotClasses[tone],
        live && dotGlow[tone]
      )}
    />
  </span>
);
