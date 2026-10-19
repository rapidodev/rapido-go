import { FC, useLayoutEffect, useRef } from "react";
import { prefersReducedMotion } from "./motion";

/**
 * A number that counts up (or down) to its value. The digits are written
 * straight into the element from requestAnimationFrame - no React state per
 * frame - and a new `value` mid-flight continues from wherever the count had
 * got to. Reduced motion (or no matchMedia, as in tests) jumps straight there.
 */
export const CountUp: FC<{
  value: number;
  format?: (n: number) => string;
  duration?: number;
  className?: string;
}> = ({ value, format = (n) => String(Math.round(n)), duration = 800, className }) => {
  const ref = useRef<HTMLSpanElement>(null);
  const current = useRef<number | null>(null);
  const fmt = useRef(format);
  fmt.current = format;

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const write = (n: number) => {
      el.textContent = fmt.current(n);
    };
    const from = current.current ?? 0;
    if (prefersReducedMotion() || from === value || !Number.isFinite(value)) {
      current.current = value;
      write(value);
      return;
    }
    const start = performance.now();
    let id = 0;
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / duration);
      const eased = 1 - Math.pow(1 - p, 3);
      current.current = from + (value - from) * eased;
      write(current.current);
      if (p < 1) id = requestAnimationFrame(tick);
      else current.current = value;
    };
    write(from);
    id = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(id);
  }, [value, duration]);

  return <span ref={ref} className={className} />;
};
