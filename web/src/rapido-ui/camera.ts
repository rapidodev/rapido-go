// The camera of the 3D world: one shared pointer handler turns the pointer
// position into two numbers in -1..1 and writes them as CSS variables on the
// scene root (--cx, --cy). Every layer scales them by its own depth in CSS, so
// there is no per-frame React state. On touch devices there is no pointer, so
// the scene drifts slowly by CSS instead, and device orientation is used only
// where the browser already allows it without asking (it never prompts).

/** The shared position, for the particle canvas, which cannot read CSS variables cheaply. */
export const camera = { x: 0, y: 0 };

const clamp = (n: number, lo = -1, hi = 1) => Math.min(hi, Math.max(lo, n));

/** Pointer position -> camera offset in -1..1 (centre of the window is 0). */
export const pointerToCamera = (
  clientX: number,
  clientY: number,
  width: number,
  height: number,
  strength = 1
): { x: number; y: number } => {
  if (!(width > 0) || !(height > 0)) return { x: 0, y: 0 };
  return {
    x: clamp((clientX / width - 0.5) * 2) * strength,
    y: clamp((clientY / height - 0.5) * 2) * strength,
  };
};

/** Device tilt (gamma left/right, beta front/back, in degrees) -> camera offset. */
export const orientationToCamera = (gamma: number | null, beta: number | null, strength = 1) => ({
  x: clamp((gamma ?? 0) / 30) * strength,
  y: clamp(((beta ?? 45) - 45) / 30) * strength,
});

/** True when the browser fires deviceorientation without a permission prompt. */
export const orientationNeedsNoPermission = (): boolean => {
  if (typeof window === "undefined" || typeof DeviceOrientationEvent === "undefined") return false;
  return typeof (DeviceOrientationEvent as unknown as { requestPermission?: unknown }).requestPermission !== "function";
};

export const startCamera = (
  root: HTMLElement,
  opts: { coarse: boolean; strength?: number }
): (() => void) => {
  const strength = opts.strength ?? 1;
  let frame = 0;
  let tx = 0;
  let ty = 0;

  const flush = () => {
    frame = 0;
    camera.x = tx;
    camera.y = ty;
    root.style.setProperty("--cx", tx.toFixed(3));
    root.style.setProperty("--cy", ty.toFixed(3));
  };
  const schedule = () => {
    if (!frame) frame = requestAnimationFrame(flush);
  };

  const onMove = (e: PointerEvent) => {
    if (e.pointerType === "touch") return;
    const p = pointerToCamera(e.clientX, e.clientY, window.innerWidth, window.innerHeight, strength);
    tx = p.x;
    ty = p.y;
    schedule();
  };
  const onOrient = (e: DeviceOrientationEvent) => {
    const p = orientationToCamera(e.gamma, e.beta, strength);
    tx = p.x;
    ty = p.y;
    schedule();
  };

  const useOrientation = opts.coarse && orientationNeedsNoPermission();
  if (!opts.coarse) window.addEventListener("pointermove", onMove, { passive: true });
  if (useOrientation) window.addEventListener("deviceorientation", onOrient, { passive: true });
  // Touch without orientation: the CSS drift carries the camera.
  root.setAttribute("data-drift", opts.coarse && !useOrientation ? "on" : "off");

  return () => {
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("deviceorientation", onOrient);
    if (frame) cancelAnimationFrame(frame);
    camera.x = camera.y = 0;
    root.style.removeProperty("--cx");
    root.style.removeProperty("--cy");
    root.setAttribute("data-drift", "off");
  };
};
