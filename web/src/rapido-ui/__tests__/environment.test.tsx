import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useAppearance } from "../appearance";
import { orientationToCamera, pointerToCamera, startCamera, camera } from "../camera";
import {
  PARTICLES,
  SceneInputs,
  createFrameMeter,
  isLowFps,
  isWeakDevice,
  resolveScene,
  useSceneRuntime,
} from "../environment";
import { World } from "../World";

const base: SceneInputs = {
  environment: "immersive",
  auto: true,
  parallax: "on",
  reducedMotion: false,
  weakDevice: false,
  lowFps: false,
};

describe("resolveScene", () => {
  it("runs the full immersive scene on a capable device", () => {
    expect(resolveScene(base)).toEqual({ level: "immersive", animated: true, parallax: true, particles: PARTICLES.immersive });
  });

  it("is nothing at all when off", () => {
    expect(resolveScene({ ...base, environment: "off" })).toEqual({ level: "off", animated: false, parallax: false, particles: 0 });
  });

  it("steps an automatic immersive scene down to subtle on a weak device or a low frame rate", () => {
    expect(resolveScene({ ...base, weakDevice: true }).level).toBe("subtle");
    expect(resolveScene({ ...base, lowFps: true }).level).toBe("subtle");
    expect(resolveScene({ ...base, weakDevice: true }).particles).toBe(PARTICLES.subtle);
  });

  it("honours an explicit choice: no automatic downgrade once the user picked a level", () => {
    expect(resolveScene({ ...base, auto: false, weakDevice: true, lowFps: true }).level).toBe("immersive");
  });

  it("never upgrades: a chosen subtle stays subtle", () => {
    expect(resolveScene({ ...base, environment: "subtle" }).level).toBe("subtle");
  });

  it("freezes the scene under reduced motion: layered but still, no camera, fewer particles", () => {
    const s = resolveScene({ ...base, reducedMotion: true });
    expect(s.level).toBe("immersive");
    expect(s.animated).toBe(false);
    expect(s.parallax).toBe(false);
    expect(s.particles).toBe(Math.round(PARTICLES.immersive / 2));
  });

  it("turns the camera off with the parallax switch", () => {
    expect(resolveScene({ ...base, parallax: "off" })).toMatchObject({ animated: true, parallax: false });
  });
});

describe("isWeakDevice", () => {
  it("is weak with four or fewer cores, or 4 GB or less of memory", () => {
    expect(isWeakDevice({ hardwareConcurrency: 4 })).toBe(true);
    expect(isWeakDevice({ hardwareConcurrency: 2 })).toBe(true);
    expect(isWeakDevice({ hardwareConcurrency: 16, deviceMemory: 4 })).toBe(true);
    expect(isWeakDevice({ hardwareConcurrency: 8, deviceMemory: 8 })).toBe(false);
  });

  it("does not guess when the numbers are missing", () => {
    expect(isWeakDevice({})).toBe(false);
    expect(isWeakDevice(undefined)).toBe(false);
    expect(isWeakDevice({ hardwareConcurrency: 0 })).toBe(false);
  });
});

describe("frame meter", () => {
  it("reports nothing until the window has passed, then the average fps", () => {
    const m = createFrameMeter(1000);
    expect(m.tick(0)).toBeNull();
    let fps: number | null = null;
    for (let t = 50; t <= 1000 && fps === null; t += 50) fps = m.tick(t); // 20 fps
    expect(fps).toBeNull();
    fps = m.tick(1050);
    expect(fps).toBeCloseTo(20, 0);
  });

  it("calls anything under the threshold low", () => {
    expect(isLowFps(20)).toBe(true);
    expect(isLowFps(58)).toBe(false);
    expect(isLowFps(null)).toBe(false);
  });
});

describe("camera maths", () => {
  it("maps the pointer to -1..1 around the window centre", () => {
    expect(pointerToCamera(500, 400, 1000, 800)).toEqual({ x: 0, y: 0 });
    expect(pointerToCamera(1000, 0, 1000, 800)).toEqual({ x: 1, y: -1 });
    expect(pointerToCamera(0, 800, 1000, 800, 0.5)).toEqual({ x: -0.5, y: 0.5 });
    expect(pointerToCamera(5, 5, 0, 0)).toEqual({ x: 0, y: 0 });
  });

  it("maps device tilt, clamped", () => {
    expect(orientationToCamera(0, 45)).toEqual({ x: 0, y: 0 });
    expect(orientationToCamera(90, 120)).toEqual({ x: 1, y: 1 });
    expect(orientationToCamera(null, null)).toEqual({ x: 0, y: 0 });
  });

  it("writes the camera as CSS variables on the root from one pointer handler, and cleans up", () => {
    const orig = window.requestAnimationFrame;
    window.requestAnimationFrame = ((cb: FrameRequestCallback) => {
      cb(0);
      return 1;
    }) as typeof window.requestAnimationFrame;
    const el = document.createElement("div");
    const stop = startCamera(el, { coarse: false });
    const ev = new MouseEvent("pointermove", { clientX: window.innerWidth, clientY: 0 });
    window.dispatchEvent(ev);
    expect(el.style.getPropertyValue("--cx")).toBe("1.000");
    expect(el.style.getPropertyValue("--cy")).toBe("-1.000");
    expect(camera.x).toBe(1);
    stop();
    expect(el.style.getPropertyValue("--cx")).toBe("");
    window.dispatchEvent(new MouseEvent("pointermove", { clientX: 0, clientY: 0 }));
    expect(el.style.getPropertyValue("--cx")).toBe("");
    window.requestAnimationFrame = orig;
  });

  it("drifts on touch where there is no orientation permission to rely on", () => {
    const el = document.createElement("div");
    const stop = startCamera(el, { coarse: true });
    expect(["on", "off"]).toContain(el.getAttribute("data-drift"));
    stop();
    expect(el.getAttribute("data-drift")).toBe("off");
  });
});

describe("World", () => {
  const setCores = (n: number | undefined) =>
    Object.defineProperty(navigator, "hardwareConcurrency", { value: n, configurable: true });

  beforeEach(() => {
    useAppearance.getState().reset();
    useSceneRuntime.getState().setLowFps(false);
    setCores(8);
  });
  afterEach(() => {
    useAppearance.getState().reset();
    useSceneRuntime.getState().setLowFps(false);
    setCores(8);
  });

  it("renders the scene at the chosen level, inert to the pointer, and static where motion is reduced (jsdom has no matchMedia)", () => {
    render(<World />);
    const w = screen.getByTestId("world");
    expect(w).toHaveAttribute("data-level", "immersive");
    expect(w).toHaveAttribute("data-animated", "false");
    expect(w).toHaveAttribute("aria-hidden", "true");
    expect(w).toHaveClass("rp-world");
  });

  it("renders nothing when the environment is off", () => {
    useAppearance.getState().set({ environment: "off" });
    render(<World />);
    expect(screen.queryByTestId("world")).toBeNull();
  });

  it("downgrades the automatic default on a weak device but not an explicit choice", () => {
    setCores(2);
    const { unmount } = render(<World />);
    expect(screen.getByTestId("world")).toHaveAttribute("data-level", "subtle");
    unmount();
    useAppearance.getState().set({ environment: "immersive", envAuto: "off" });
    render(<World />);
    expect(screen.getByTestId("world")).toHaveAttribute("data-level", "immersive");
  });

  it("steps down when the measured frame rate was low", () => {
    render(<World />);
    expect(screen.getByTestId("world")).toHaveAttribute("data-level", "immersive");
    act(() => useSceneRuntime.getState().setLowFps(true));
    expect(screen.getByTestId("world")).toHaveAttribute("data-level", "subtle");
  });
});

describe("environment setting plumbing", () => {
  it("defaults to immersive, automatic, with the camera on", () => {
    useAppearance.getState().reset();
    const s = useAppearance.getState();
    expect([s.environment, s.envAuto, s.parallax]).toEqual(["immersive", "on", "on"]);
    expect(document.documentElement.getAttribute("data-environment")).toBe("immersive");
    expect(document.documentElement.getAttribute("data-parallax")).toBe("on");
  });

  it("persists the choices and applies them to <html>", () => {
    useAppearance.getState().set({ environment: "subtle", envAuto: "off", parallax: "off" });
    const stored = JSON.parse(window.localStorage.getItem("rapido.appearance") as string);
    expect(stored).toMatchObject({ environment: "subtle", envAuto: "off", parallax: "off" });
    expect(document.documentElement.getAttribute("data-environment")).toBe("subtle");
    expect(document.documentElement.getAttribute("data-parallax")).toBe("off");
    useAppearance.getState().reset();
  });
});
