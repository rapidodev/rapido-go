import { PointerEvent, RefObject, useCallback, useEffect, useRef } from "react";
import { useAppearance } from "./appearance";
import { useReducedMotion } from "./motion";

/**
 * Pointer tilt for a card, as plain CSS variables: one handler measures the
 * pointer against the element and writes --rp-rx / --rp-ry (the rotation, in
 * degrees) and --rp-mx / --rp-my (where the glare sits) straight onto the
 * element. No React state, so moving the mouse never re-renders anything; the
 * `.rp-tilt` class in tailwind.css turns the variables into a transform while
 * the card is hovered. Touch input and reduced motion get no tilt at all.
 *
 * It is the one shared piece behind every tilting surface (Card's `tilt`
 * prop) so the maths and the guards live in a single place.
 */
export type TiltOptions = {
  /** Largest rotation in degrees, reached at the card's edge. */
  max?: number;
  enabled?: boolean;
};

export type TiltBindings<T extends HTMLElement> = {
  ref: RefObject<T>;
  onPointerMove: (e: PointerEvent<T>) => void;
  onPointerLeave: () => void;
};

const VARS = ["--rp-rx", "--rp-ry", "--rp-mx", "--rp-my"] as const;

/** Rotation for a pointer at fractional position (px, py) inside the element,
 * both in 0..1. Pointer above centre tips the top edge away (positive rotateX
 * is top-away), pointer right of centre turns the right edge away. */
export const tiltAngles = (px: number, py: number, max: number) => {
  const clamp = (n: number) => Math.min(1, Math.max(0, n));
  const x = clamp(px);
  const y = clamp(py);
  return { rx: (0.5 - y) * 2 * max, ry: (x - 0.5) * 2 * max };
};

export const useTilt = <T extends HTMLElement = HTMLDivElement>({
  max = 4,
  enabled = true,
}: TiltOptions = {}): TiltBindings<T> => {
  const ref = useRef<T>(null);
  const frame = useRef(0);
  const reduced = useReducedMotion();
  // 3D depth: off = no tilt, subtle = half the angle, full = as asked.
  const depth = useAppearance((s) => s.depth);
  const active = enabled && !reduced && depth !== "off";
  const scale = depth === "subtle" ? 0.5 : 1;

  const reset = useCallback(() => {
    if (frame.current) cancelAnimationFrame(frame.current);
    frame.current = 0;
    const el = ref.current;
    if (el) for (const v of VARS) el.style.removeProperty(v);
  }, []);

  useEffect(() => reset, [reset]);

  const onPointerMove = useCallback(
    (e: PointerEvent<T>) => {
      if (!active || e.pointerType === "touch" || frame.current) return;
      const { clientX, clientY } = e;
      frame.current = requestAnimationFrame(() => {
        frame.current = 0;
        const el = ref.current;
        if (!el) return;
        const r = el.getBoundingClientRect();
        if (r.width === 0 || r.height === 0) return;
        const px = (clientX - r.left) / r.width;
        const py = (clientY - r.top) / r.height;
        const { rx, ry } = tiltAngles(px, py, max * scale);
        el.style.setProperty("--rp-rx", `${rx.toFixed(2)}deg`);
        el.style.setProperty("--rp-ry", `${ry.toFixed(2)}deg`);
        el.style.setProperty("--rp-mx", `${(px * 100).toFixed(1)}%`);
        el.style.setProperty("--rp-my", `${(py * 100).toFixed(1)}%`);
      });
    },
    [active, max, scale]
  );

  return { ref, onPointerMove, onPointerLeave: active ? reset : () => {} };
};
