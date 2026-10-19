import { FC, useEffect, useRef } from "react";
import { camera } from "./camera";
import { createFrameMeter, isLowFps, useSceneRuntime } from "./environment";

// A field of fine particles at different depths on ONE canvas. Far ones are
// small, dim and soft (a blurred sprite stands in for depth of field), near ones
// crisp and a little bigger; the camera shifts near ones more. The count is
// capped by the caller, the loop stops when the tab is hidden, and a slow start
// reports a low frame rate so the scene can step down.

type P = { x: number; y: number; z: number; vy: number; tw: number };

const sprite = (rgb: string, radius: number, soft: boolean): HTMLCanvasElement => {
  const size = Math.ceil(radius * 2 + 2);
  const c = document.createElement("canvas");
  c.width = c.height = size;
  const g = c.getContext("2d");
  if (g) {
    const grad = g.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
    grad.addColorStop(0, `rgba(${rgb},1)`);
    grad.addColorStop(soft ? 0.25 : 0.7, `rgba(${rgb},${soft ? 0.5 : 0.95})`);
    grad.addColorStop(1, `rgba(${rgb},0)`);
    g.fillStyle = grad;
    g.fillRect(0, 0, size, size);
  }
  return c;
};

export const ParticleField: FC<{
  count: number;
  animated: boolean;
  /** Measure the first seconds and report a low frame rate. */
  watchFps: boolean;
  /** Re-reads the colour when the theme or accent changes. */
  tint: string;
}> = ({ count, animated, watchFps, tint }) => {
  const ref = useRef<HTMLCanvasElement>(null);
  const setLowFps = useSceneRuntime((s) => s.setLowFps);

  useEffect(() => {
    const canvas = ref.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx || count <= 0) return;

    const rgbRaw = getComputedStyle(document.documentElement).getPropertyValue("--rp-accent").trim() || "34 211 238";
    const rgb = rgbRaw.split(/\s+/).join(",");
    const soft = sprite(rgb, 9, true);
    const crisp = sprite(rgb, 2.4, false);

    let w = 0;
    let h = 0;
    const resize = () => {
      w = canvas.width = window.innerWidth;
      h = canvas.height = window.innerHeight;
    };
    resize();
    window.addEventListener("resize", resize);

    const parts: P[] = Array.from({ length: count }, () => ({
      x: Math.random(),
      y: Math.random(),
      z: 0.1 + Math.random() * 0.9,
      vy: 0.004 + Math.random() * 0.012,
      tw: Math.random() * Math.PI * 2,
    }));

    const draw = (t: number, dt: number) => {
      ctx.clearRect(0, 0, w, h);
      for (const p of parts) {
        if (animated) {
          p.y -= p.vy * p.z * dt;
          if (p.y < -0.02) {
            p.y = 1.02;
            p.x = Math.random();
          }
        }
        const far = p.z < 0.4;
        const px = p.x * w + (animated ? camera.x * p.z * -46 : 0);
        const py = p.y * h + (animated ? camera.y * p.z * -28 : 0);
        const tw = animated ? 0.75 + 0.25 * Math.sin(t / 900 + p.tw) : 1;
        ctx.globalAlpha = (far ? 0.28 : 0.32 + p.z * 0.55) * tw;
        const img = far ? soft : crisp;
        const s = far ? img.width * (0.45 + p.z) : img.width * (0.6 + p.z * 0.9);
        ctx.drawImage(img, px - s / 2, py - s / 2, s, s);
      }
      ctx.globalAlpha = 1;
    };

    if (!animated) {
      draw(0, 0);
      return () => window.removeEventListener("resize", resize);
    }

    const meter = watchFps ? createFrameMeter() : null;
    let raf = 0;
    let last = 0;
    let metered = !watchFps;
    const loop = (now: number) => {
      const dt = last ? Math.min(0.1, (now - last) / 1000) : 0;
      last = now;
      draw(now, dt);
      if (!metered && meter) {
        const fps = meter.tick(now);
        if (fps !== null) {
          metered = true;
          if (isLowFps(fps)) setLowFps(true);
        }
      }
      raf = requestAnimationFrame(loop);
    };
    const start = () => {
      if (!raf && !document.hidden) {
        last = 0;
        raf = requestAnimationFrame(loop);
      }
    };
    const stop = () => {
      if (raf) cancelAnimationFrame(raf);
      raf = 0;
    };
    const onVis = () => (document.hidden ? stop() : start());
    document.addEventListener("visibilitychange", onVis);
    start();
    return () => {
      stop();
      document.removeEventListener("visibilitychange", onVis);
      window.removeEventListener("resize", resize);
    };
  }, [count, animated, watchFps, tint, setLowFps]);

  return <canvas ref={ref} className="rp-w-particles" aria-hidden="true" />;
};
