import { FC, PropsWithChildren, ReactNode } from "react";
import classNames from "classnames";
import { Card, CardTitle } from "./Card";

// Promotes the Overlay+ModalCard pair that used to live only inside
// UserActionModals.tsx (and was hand-duplicated, slightly differently sized,
// inline in UserFormModal.tsx and AdminsAdmin.tsx's AdminForm). The
// click-outside-to-close check matters: it compares `e.target ===
// e.currentTarget` rather than just listening for any click on the overlay,
// so a mousedown that starts inside the card and is dragged/released outside
// it (selecting text, for instance) does not close the modal.
export type ModalProps = PropsWithChildren<{
  onClose: () => void;
  /** Rendered via CardTitle above `children` when given. Omit it to build a
   * fully custom header (a title plus a subtitle plus a close button, say)
   * as part of `children` instead. */
  title?: ReactNode;
  /** Overrides the card's default `max-w-sm` sizing, e.g. "max-w-2xl". */
  className?: string;
}>;

export const Modal: FC<ModalProps> = ({ onClose, title, className, children }) => (
  <div
    className="fixed inset-0 z-50 flex animate-rp-fade items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
    onMouseDown={(e) => {
      if (e.target === e.currentTarget) onClose();
    }}
  >
    {/* The width sits on a wrapper: Tailwind orders same-property utilities by name, not size, so
        on the card a caller's max-w-2xl would lose to both max-w-sm and the card's own max-w-full. */}
    <div className={classNames("w-full", className ?? "max-w-sm")}>
      <Card
        elevation={3}
        className="max-h-[90vh] animate-rp-rise overflow-y-auto border-rapido-accent/20 bg-rapido-surface"
      >
        {title !== undefined && <CardTitle className="mb-3 text-base">{title}</CardTitle>}
        {children}
      </Card>
    </div>
  </div>
);
