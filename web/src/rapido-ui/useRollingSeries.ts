import { useMemo } from "react";

// A client-side rolling buffer of the last N samples of a metric, so a card can
// draw a trend from nothing but the numbers the panel already polls. It lives
// at module level, keyed by the caller, so leaving the page and coming back
// keeps the history instead of starting a flat line again.

type Entry = { stamp: unknown; values: (number | null)[] };
const store = new Map<string, Entry>();

export const SERIES_LIMIT = 40;

/** Adds a sample unless this `stamp` (the poll's own timestamp) was already recorded. */
export const pushSample = (
  key: string,
  value: number | null | undefined,
  stamp: unknown,
  limit = SERIES_LIMIT
): (number | null)[] => {
  const prev = store.get(key);
  if (prev && prev.stamp === stamp) return prev.values;
  const sample = typeof value === "number" && Number.isFinite(value) ? value : null;
  const values = [...(prev?.values ?? []), sample].slice(-limit);
  store.set(key, { stamp, values });
  return values;
};

export const clearSeries = (): void => store.clear();

/**
 * The rolling series for `key`. A new sample is recorded each time `stamp`
 * changes (use the metrics' own `checked_at`, so an unrelated re-render or a
 * poll that returned the same snapshot adds nothing). A missing reading is
 * recorded as a gap (null), not a zero.
 */
export const useRollingSeries = (
  key: string,
  value: number | null | undefined,
  stamp: unknown,
  limit = SERIES_LIMIT
): (number | null)[] => useMemo(() => pushSample(key, value, stamp, limit), [key, value, stamp, limit]);
