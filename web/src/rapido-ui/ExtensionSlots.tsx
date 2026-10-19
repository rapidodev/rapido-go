import { FC, Suspense } from "react";
import { NodeLinksSlotProps, TunnelNodeSlotProps, extensionSlots } from "extensions";

/** Renders whatever add-ons registered for the tunnel card's node zone; nothing when none did. */
export const TunnelNodeSlot: FC<TunnelNodeSlotProps> = (props) => (
  <>
    {extensionSlots("tunnelNodeFooter").map((Slot, i) => (
      <Suspense key={i} fallback={null}>
        <Slot {...props} />
      </Suspense>
    ))}
  </>
);

/** Renders whatever add-ons registered for a node card's links zone; nothing when none did. */
export const NodeLinksSlot: FC<NodeLinksSlotProps> = (props) => (
  <>
    {extensionSlots("nodeLinksFooter").map((Slot, i) => (
      <Suspense key={i} fallback={null}>
        <Slot {...props} />
      </Suspense>
    ))}
  </>
);
