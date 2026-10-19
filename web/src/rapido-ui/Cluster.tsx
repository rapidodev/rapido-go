import { FC, HTMLAttributes, ReactNode } from "react";
import classNames from "classnames";

// The instrument cluster: ONE inset plate divided into zones by hairlines, like
// the dashboard behind a steering wheel - not a grid of separate cards. The
// hairlines are the grid's own gap showing the plate colour through, so they
// stay correct however the zones wrap (a row on a wide screen, a stack on a
// phone) and in RTL. Zones share the inset surface step of the language
// (see instrument.ts), and every zone has one quiet title line.

export const Cluster: FC<HTMLAttributes<HTMLDivElement> & { minZone?: string; fixed?: boolean }> = ({
  className,
  minZone = "15.5rem",
  fixed = false,
  style,
  children,
  ...rest
}) => (
  <div
    className={classNames(
      "grid gap-px overflow-hidden rounded-[calc(var(--rp-r-card)-0.35rem)] border border-rapido-border/70 bg-rapido-border/60 shadow-rp-inset",
      className
    )}
    // `fixed`: the caller lays the columns out with classes (grid-cols-*), so the
    // zones can span rows and no cell is left empty; otherwise zones simply wrap.
    style={fixed ? style : { gridTemplateColumns: `repeat(auto-fit, minmax(${minZone}, 1fr))`, ...style }}
    {...rest}
  >
    {children}
  </div>
);

export const ClusterZone: FC<{
  title: ReactNode;
  /** Small chips next to the title (the network card, a status). */
  chips?: ReactNode;
  children: ReactNode;
  className?: string;
}> = ({ title, chips, children, className }) => (
  <section className={classNames("flex min-w-0 flex-col gap-3 bg-rapido-inset p-4", className)}>
    <div className="flex min-h-[1.5rem] flex-wrap items-center gap-x-2 gap-y-1">
      <h3 className="text-[11px] font-semibold uppercase tracking-wider text-rapido-muted">{title}</h3>
      {chips}
    </div>
    {children}
  </section>
);
