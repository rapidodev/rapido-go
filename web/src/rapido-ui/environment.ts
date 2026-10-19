import { create } from "zustand";
import { Appearance } from "./appearance";

// The decision logic behind the 3D world, kept pure so it can be tested: what
// level the scene actually runs at, given the user's choice and what the device
// and the frame rate can carry.

export type SceneLevel = "off" | "subtle" | "immersive";

export type SceneInputs = {
  environment: Appearance["environment"];
  /** True while the level may be stepped down automatically. */
  auto: boolean;
  parallax: Appearance["parallax"];
  /** Motion is "reduced" or "none" (the Appearance motion setting, or the OS). */
  reducedMotion: boolean;
  weakDevice: boolean;
  /** The measured frame rate over the first seconds was too low. */
  lowFps: boolean;
};

export type Scene = {
  level: SceneLevel;
  /** Anything moves at all (floor drift, floating objects, particles, camera). */
  animated: boolean;
  /** The camera follows the pointer / drifts. */
  parallax: boolean;
  /** Particles drawn (a still field when not animated). */
  particles: number;
};

export const PARTICLES = { subtle: 28, immersive: 90 } as const;

/** Weak = few cores or little memory. Missing numbers are not treated as weak. */
export const isWeakDevice = (nav: { hardwareConcurrency?: number; deviceMemory?: number } | undefined): boolean => {
  if (!nav) return false;
  const cores = nav.hardwareConcurrency;
  const mem = nav.deviceMemory;
  return (typeof cores === "number" && cores > 0 && cores <= 4) || (typeof mem === "number" && mem > 0 && mem <= 4);
};

export const resolveScene = (i: SceneInputs): Scene => {
  if (i.environment === "off") return { level: "off", animated: false, parallax: false, particles: 0 };
  let level: SceneLevel = i.environment;
  if (i.auto && level === "immersive" && (i.weakDevice || i.lowFps)) level = "subtle";
  // Reduced motion keeps the layered scene but freezes it.
  const animated = !i.reducedMotion;
  const particles = PARTICLES[level];
  return {
    level,
    animated,
    parallax: animated && i.parallax === "on",
    particles: animated ? particles : Math.round(particles / 2),
  };
};

/** Collects frame times for a short window and answers whether it ran too slowly. */
export const createFrameMeter = (windowMs = 2500, minFrames = 20) => {
  let start = 0;
  let last = 0;
  let frames = 0;
  return {
    /** Feed each animation frame's timestamp; returns the average fps once the
     * window has elapsed, otherwise null. */
    tick(now: number): number | null {
      if (!start) {
        start = last = now;
        return null;
      }
      frames++;
      last = now;
      if (now - start < windowMs) return null;
      return frames >= minFrames || frames > 0 ? (frames * 1000) / (last - start) : null;
    },
  };
};

export const isLowFps = (fps: number | null, threshold = 28): boolean => fps !== null && fps < threshold;

// Runtime flag set by the particle loop when it measures a slow start. It is
// per page load on purpose: a different tab or a quieter moment may run fine.
export const useSceneRuntime = create<{ lowFps: boolean; setLowFps: (v: boolean) => void }>((set) => ({
  lowFps: false,
  setLowFps: (lowFps) => set({ lowFps }),
}));
