import { useMemo, useRef } from "react";
import { autoMax, isNumber, nextPeak } from "utils/tunnelMetrics";

/**
 * The maximum for a dial that has no natural limit: it follows the highest
 * value seen (smoothed, see nextPeak) with headroom, rounded to a round number
 * so the tick labels stay readable. Updates only when `value` changes, so an
 * unrelated re-render does not age the peak.
 */
export const useAutoMax = (value: number | null | undefined, floor: number): { max: number; peak: number } => {
  const peak = useRef(0);
  return useMemo(() => {
    if (isNumber(value)) peak.current = nextPeak(peak.current, value);
    return { max: autoMax(peak.current, floor), peak: peak.current };
  }, [value, floor]);
};
