import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ACCENTS,
  DEFAULT_APPEARANCE,
  STORAGE_KEY,
  applyAppearance,
  currentAppearance,
  loadAppearance,
  resolveMotion,
  sanitizeAppearance,
  useAppearance,
} from "../appearance";
import { AppearancePanel } from "../AppearancePanel";
import { ResourceMeter } from "../ResourceMeter";

beforeEach(() => {
  window.localStorage.clear();
  useAppearance.getState().reset();
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("appearance defaults and sanitising", () => {
  it("defaults to the full look: cockpit theme, full 3D, ring gauges", () => {
    expect(DEFAULT_APPEARANCE).toMatchObject({
      theme: "cockpit",
      accent: "theme",
      depth: "full",
      environment: "immersive",
      envAuto: "on",
      parallax: "on",
      motion: "auto",
      meters: "gauges",
      dial: "ring",
      radius: "rounded",
      density: "comfortable",
    });
  });

  it("keeps valid values, replaces invalid ones with the default and drops unknown keys", () => {
    const out = sanitizeAppearance({ theme: "daylight", depth: "huge", bogus: 1, radius: "pill" });
    expect(out.theme).toBe("daylight");
    expect(out.radius).toBe("pill");
    expect(out.depth).toBe(DEFAULT_APPEARANCE.depth);
    expect(out).not.toHaveProperty("bogus");
  });

  it("copes with garbage in storage", () => {
    window.localStorage.setItem(STORAGE_KEY, "{not json");
    expect(loadAppearance()).toEqual(DEFAULT_APPEARANCE);
    window.localStorage.setItem(STORAGE_KEY, "42");
    expect(loadAppearance()).toEqual(DEFAULT_APPEARANCE);
  });
});

describe("persistence", () => {
  it("saves each choice to localStorage and reads it back", () => {
    useAppearance.getState().set({ theme: "graphite", density: "compact" });
    const stored = JSON.parse(window.localStorage.getItem(STORAGE_KEY) as string);
    expect(stored).toMatchObject({ theme: "graphite", density: "compact", depth: "full" });
    expect(loadAppearance()).toMatchObject({ theme: "graphite", density: "compact" });
  });

  it("reset clears the stored choice and returns to defaults", () => {
    useAppearance.getState().set({ theme: "aurora" });
    useAppearance.getState().reset();
    expect(window.localStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(currentAppearance()).toEqual(DEFAULT_APPEARANCE);
    expect(document.documentElement.getAttribute("data-theme")).toBe("cockpit");
  });

  it("still applies the choice when storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => useAppearance.getState().set({ theme: "daylight" })).not.toThrow();
    expect(document.documentElement.getAttribute("data-theme")).toBe("daylight");
  });

  it("falls back to defaults when reading storage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(loadAppearance()).toEqual(DEFAULT_APPEARANCE);
  });
});

describe("applying to <html>", () => {
  const apply = (patch: Partial<typeof DEFAULT_APPEARANCE>, systemReduced = false) => {
    const el = document.createElement("div");
    applyAppearance({ ...DEFAULT_APPEARANCE, ...patch }, el, systemReduced);
    return el;
  };

  it("writes every choice as a data attribute", () => {
    const el = apply({ theme: "aurora", depth: "subtle", environment: "subtle", parallax: "off", meters: "bars", dial: "ring", density: "compact", radius: "sharp" });
    expect(el.dataset).toMatchObject({
      theme: "aurora",
      depth: "subtle",
      environment: "subtle",
      parallax: "off",
      meters: "bars",
      dial: "ring",
      density: "compact",
      radius: "sharp",
    });
  });

  it("resolves motion: auto follows the OS, the others are explicit", () => {
    expect(apply({ motion: "auto" }, true).dataset.motion).toBe("reduced");
    expect(apply({ motion: "auto" }, false).dataset.motion).toBe("full");
    expect(apply({ motion: "full" }, true).dataset.motion).toBe("full");
    expect(apply({ motion: "none" }, false).dataset.motion).toBe("none");
    expect(resolveMotion("reduced", false)).toBe("reduced");
  });

  it("sets the accent variables for a chosen swatch and removes them for the theme default", () => {
    const el = apply({ accent: "rose" });
    expect(el.style.getPropertyValue("--rp-accent")).toBe(ACCENTS.rose.accent);
    expect(el.style.getPropertyValue("--rp-on-accent")).toBe(ACCENTS.rose.on);
    applyAppearance({ ...DEFAULT_APPEARANCE, accent: "theme" }, el, false);
    expect(el.style.getPropertyValue("--rp-accent")).toBe("");
  });

  it("uses the darker accent shade on the light preset so it stays readable on white", () => {
    const el = apply({ theme: "daylight", accent: "amber" });
    expect(el.style.getPropertyValue("--rp-accent")).toBe(ACCENTS.amber.lo);
  });

  it("tints the browser chrome to the preset", () => {
    const meta = document.createElement("meta");
    meta.setAttribute("name", "theme-color");
    document.head.appendChild(meta);
    apply({ theme: "daylight" });
    expect(meta.getAttribute("content")).toBe("#eef2f7");
    meta.remove();
  });
});

describe("AppearancePanel", () => {
  it("offers four presets and eight accent swatches", () => {
    render(<AppearancePanel />);
    const themes = screen.getByRole("radiogroup", { name: "Theme" });
    expect(within(themes).getAllByRole("radio")).toHaveLength(4);
    const accents = screen.getByRole("radiogroup", { name: "Accent colour" });
    expect(within(accents).getAllByRole("radio")).toHaveLength(8);
  });

  it("applies a choice at once, without a reload", () => {
    render(<AppearancePanel />);
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "Theme" })).getByRole("radio", { name: /Daylight/ }));
    expect(document.documentElement.getAttribute("data-theme")).toBe("daylight");
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "3D depth" })).getByRole("radio", { name: "Off" }));
    expect(document.documentElement.getAttribute("data-depth")).toBe("off");
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "Corner radius" })).getByRole("radio", { name: "Sharp" }));
    expect(document.documentElement.getAttribute("data-radius")).toBe("sharp");
  });

  it("reset puts everything back", () => {
    render(<AppearancePanel />);
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "Density" })).getByRole("radio", { name: "Compact" }));
    expect(document.documentElement.getAttribute("data-density")).toBe("compact");
    fireEvent.click(screen.getByRole("button", { name: "Reset to default" }));
    expect(document.documentElement.getAttribute("data-density")).toBe("comfortable");
  });

  it("switches the live preview between a gauge and a classic bar", () => {
    render(<AppearancePanel />);
    const preview = screen.getByTestId("appearance-preview");
    expect(within(preview).getByRole("img")).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole("radiogroup", { name: "Resource meters" })).getByRole("radio", { name: "Classic bars" }));
    expect(within(preview).queryByRole("img")).toBeNull();
    expect(within(preview).getByRole("meter")).toBeInTheDocument();
  });
});

describe("ResourceMeter", () => {
  it("renders a dial by default and a labelled bar in bars mode", () => {
    const { rerender } = render(<ResourceMeter value={40} max={100} unit="%" label="CPU" />);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "CPU: 40 %");
    useAppearance.getState().set({ meters: "bars" });
    rerender(<ResourceMeter value={40} max={100} unit="%" label="CPU" />);
    const bar = screen.getByRole("meter", { name: "CPU" });
    expect(bar).toHaveAttribute("aria-valuenow", "40");
    expect(bar).toHaveAttribute("aria-valuetext", "40 %");
  });

  it("can be pinned to one rendering regardless of the global switch", () => {
    useAppearance.getState().set({ meters: "bars" });
    render(<ResourceMeter mode="gauge" value={10} max={100} label="x" />);
    expect(screen.getByRole("img")).toBeInTheDocument();
  });

  it("shows dashes in bar mode when the value is unknown", () => {
    render(<ResourceMeter mode="bar" value={null} max={100} label="RAM" />);
    expect(screen.getByRole("meter", { name: "RAM" })).not.toHaveAttribute("aria-valuenow");
  });
});
