import { CSSProperties, FC, useEffect, useMemo, useRef, useState } from "react";
import { useAppearance } from "./appearance";
import { startCamera } from "./camera";
import { isWeakDevice, resolveScene, useSceneRuntime } from "./environment";
import { useReducedMotion } from "./motion";
import { ParticleField } from "./ParticleField";

// The world every page sits in: a sky, a glowing grid floor running to a
// horizon with haze, objects floating at several depths and a field of
// particles. It is mounted once at the app root (so moving between pages does
// not restart it) and is never in the way: it ignores pointer events, only
// animates transform and opacity, stops when the tab is hidden, and steps down
// by itself on weak devices or a slow start. The look follows the theme preset
// (see the --w-* variables in tailwind.css).

type Obj = { cls: string; style: CSSProperties; color: string; immersive?: boolean };

const o = (
  kind: "orb" | "ring" | "shard" | "beam",
  box: { left?: string; right?: string; top?: string; bottom?: string; size: string; h?: string },
  depth: number,
  color: "accent" | "violet" | "teal",
  immersive = false,
  extra?: CSSProperties
): Obj => ({
  cls: `rp-w-${kind}`,
  color: `var(--rp-${color})`,
  immersive,
  style: {
    left: box.left,
    right: box.right,
    top: box.top,
    bottom: box.bottom,
    width: box.size,
    height: box.h ?? box.size,
    ["--depth" as string]: depth,
    ...extra,
  },
});

// Far things are big, soft and move little; near things are small, crisp and move a lot.
const OBJECTS: Obj[] = [
  o("orb", { left: "52%", top: "46%", size: "46vmin" }, 0.18, "teal", false, { filter: "blur(10px)" }),
  o("orb", { left: "6%", top: "12%", size: "30vmin" }, 0.45, "accent"),
  o("orb", { right: "9%", top: "8%", size: "20vmin" }, 0.8, "violet", true),
  o("orb", { right: "30%", top: "58%", size: "7vmin" }, 1.3, "accent", true),
  o("ring", { right: "6%", top: "28%", size: "36vmin" }, 0.6, "accent", true),
  o("ring", { left: "9%", top: "56%", size: "22vmin" }, 0.95, "violet", true),
  o("shard", { left: "24%", top: "18%", size: "5vmin", h: "13vmin" }, 1.1, "accent", true),
  o("shard", { right: "22%", top: "12%", size: "4vmin", h: "10vmin" }, 0.7, "violet", true),
  o("shard", { left: "70%", top: "34%", size: "3vmin", h: "8vmin" }, 1.4, "teal", true),
  o("beam", { left: "18%", bottom: "56%", size: "8vmin", h: "44vh" }, 0.3, "accent", true),
  o("beam", { left: "76%", bottom: "56%", size: "6vmin", h: "36vh" }, 0.4, "violet", true),
];

const pageOf = (hash: string): string => {
  const p = hash.replace(/^#\/?/, "").split("/")[0];
  return p === "" ? "overview" : p;
};

export const World: FC = () => {
  const environment = useAppearance((s) => s.environment);
  const envAuto = useAppearance((s) => s.envAuto);
  const parallax = useAppearance((s) => s.parallax);
  const theme = useAppearance((s) => s.theme);
  const accent = useAppearance((s) => s.accent);
  const reduced = useReducedMotion();
  const lowFps = useSceneRuntime((s) => s.lowFps);
  const weakDevice = useMemo(
    () => isWeakDevice(typeof navigator === "undefined" ? undefined : (navigator as unknown as { hardwareConcurrency?: number; deviceMemory?: number })),
    []
  );
  const scene = resolveScene({
    environment,
    auto: envAuto === "on",
    parallax,
    reducedMotion: reduced,
    weakDevice,
    lowFps,
  });

  const root = useRef<HTMLDivElement>(null);
  const [page, setPage] = useState(() => (typeof window === "undefined" ? "overview" : pageOf(window.location.hash)));
  const coarse = useMemo(
    () => typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches,
    []
  );

  // the camera
  useEffect(() => {
    const el = root.current;
    if (!el || scene.level === "off" || !scene.parallax) return;
    return startCamera(el, { coarse, strength: scene.level === "subtle" ? 0.5 : 1 });
  }, [scene.level, scene.parallax, coarse]);

  // nothing runs in a hidden tab
  useEffect(() => {
    const sync = () => root.current?.setAttribute("data-paused", String(document.hidden));
    document.addEventListener("visibilitychange", sync);
    sync();
    return () => document.removeEventListener("visibilitychange", sync);
  });

  // moving between pages: a short fly through the scene, and the page's own horizon
  useEffect(() => {
    let timer = 0;
    const onHash = () => {
      setPage(pageOf(window.location.hash));
      const el = root.current;
      if (!el) return;
      el.setAttribute("data-fly", "0");
      void el.offsetWidth; // restart the animation
      el.setAttribute("data-fly", "1");
      window.clearTimeout(timer);
      timer = window.setTimeout(() => el.setAttribute("data-fly", "0"), 800);
    };
    window.addEventListener("hashchange", onHash);
    return () => {
      window.removeEventListener("hashchange", onHash);
      window.clearTimeout(timer);
    };
  }, []);

  if (scene.level === "off") return null;

  return (
    <div
      ref={root}
      className="rp-world"
      aria-hidden="true"
      data-testid="world"
      data-level={scene.level}
      data-animated={scene.animated ? "true" : "false"}
      data-page={page}
    >
      <div className="rp-w-sky" />
      <div className="rp-w-cam">
        <div className="rp-w-haze" />
        <div className="rp-w-horizon-line" />
        <div className="rp-w-floor-wrap">
          <div className="rp-w-floor">
            <div className="rp-w-grid" />
          </div>
        </div>
        <div className="rp-w-floorglow" />
        {OBJECTS.map((obj, i) => (
          <div
            key={i}
            className={`rp-w-obj${obj.immersive ? " rp-only-immersive" : ""}`}
            style={{ ...obj.style, ["--orb" as string]: obj.color }}
          >
            <div className={obj.cls} />
          </div>
        ))}
      </div>
      <ParticleField count={scene.particles} animated={scene.animated} watchFps={scene.animated && envAuto === "on" && scene.level === "immersive"} tint={`${theme}:${accent}`} />
      <div className="rp-w-vignette" />
    </div>
  );
};
