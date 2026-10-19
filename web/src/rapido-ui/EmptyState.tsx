import { FC, ReactNode } from "react";
import classNames from "classnames";
import { Card } from "./Card";

/** "Nothing here yet" - an icon, a line of what this is, why it is empty, and
 * the one action that fills it. */
export const EmptyState: FC<{
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  className?: string;
}> = ({ icon, title, description, action, className }) => (
  <Card
    elevation={1}
    className={classNames("flex flex-col items-center gap-2 border-dashed px-6 py-10 text-center", className)}
  >
    {icon && (
      <div
        aria-hidden="true"
        className="mb-1 flex h-12 w-12 items-center justify-center rounded-full bg-rapido-accent/10 text-rapido-accent shadow-rp-glow [&>svg]:h-6 [&>svg]:w-6"
      >
        {icon}
      </div>
    )}
    <div className="text-sm font-semibold text-rapido-text">{title}</div>
    {description && <div className="max-w-md text-xs text-rapido-muted">{description}</div>}
    {action && <div className="mt-2">{action}</div>}
  </Card>
);
