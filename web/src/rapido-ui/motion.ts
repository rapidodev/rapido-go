import { useEffect, useState } from "react";
import { resolveMotion, systemPrefersReducedMotion, useAppearance } from "./appearance";

const QUERY = "(prefers-reduced-motion: reduce)";

/** One-shot read, for code that is not a component (the count-up loop). The
 * appearance choice wins; "auto" follows the OS. Where matchMedia does not
 * exist (jsdom, old WebViews) the answer is "reduce": a decorative animation
 * is never worth guessing on. */
export const prefersReducedMotion = (): boolean =>
  resolveMotion(useAppearance.getState().motion, systemPrefersReducedMotion()) !== "full";

/** Live version of the above: re-renders if either the appearance choice or
 * the OS setting changes while the page is open. */
export const useReducedMotion = (): boolean => {
  const motion = useAppearance((s) => s.motion);
  const [system, setSystem] = useState(systemPrefersReducedMotion);
  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia(QUERY);
    const onChange = () => setSystem(mq.matches);
    onChange();
    if (mq.addEventListener) mq.addEventListener("change", onChange);
    else mq.addListener(onChange);
    return () => {
      if (mq.removeEventListener) mq.removeEventListener("change", onChange);
      else mq.removeListener(onChange);
    };
  }, []);
  return resolveMotion(motion, system) !== "full";
};
