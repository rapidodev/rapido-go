import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { DEFAULT_APPEARANCE, useAppearance } from "../appearance";
import {
  arcPath,
  formatCompact,
  fractionOf,
  Gauge,
  LOAD_ZONES,
  solidZones,
  zoneColorAt,
} from "../Gauge";

describe("gauge maths", () => {
  it("maps a value to a clamped fraction of the range", () => {
    expect(fractionOf(50, 0, 100)).toBe(0.5);
    expect(fractionOf(150, 0, 100)).toBe(1);
    expect(fractionOf(-5, 0, 100)).toBe(0);
    expect(fractionOf(15, 10, 20)).toBe(0.5);
  });

  it("treats an empty range or a non-number as zero rather than NaN", () => {
    expect(fractionOf(5, 10, 10)).toBe(0);
    expect(fractionOf(Number.NaN, 0, 100)).toBe(0);
  });

  it("picks the zone a fraction falls in, including the boundaries", () => {
    expect(zoneColorAt(LOAD_ZONES, 0)).toBe(LOAD_ZONES[0].color);
    expect(zoneColorAt(LOAD_ZONES, 0.7)).toBe(LOAD_ZONES[0].color);
    expect(zoneColorAt(LOAD_ZONES, 0.71)).toBe(LOAD_ZONES[1].color);
    expect(zoneColorAt(LOAD_ZONES, 1)).toBe(LOAD_ZONES[2].color);
    expect(zoneColorAt(solidZones("#123456"), 0.4)).toBe("#123456");
  });

  it("draws the full 270 degree sweep as a large arc and a short one as a small arc", () => {
    expect(arcPath(72, 0, 1)).toMatch(/ 0 1 1 /);
    expect(arcPath(72, 0, 0.3)).toMatch(/ 0 0 1 /);
  });

  it("compacts big numbers for tick labels", () => {
    expect(formatCompact(40)).toBe("40");
    expect(formatCompact(1200)).toBe("1.2k");
    expect(formatCompact(3_500_000)).toBe("3.5M");
    expect(formatCompact(1e9)).toBe("1G");
  });
});

describe("Gauge", () => {
  // The analog dial is the selectable alternative to the default ring.
  beforeEach(() => useAppearance.getState().set({ dial: "needle" }));
  afterEach(() => useAppearance.getState().reset());

  it("is an image whose accessible name states the value", () => {
    render(<Gauge value={42} max={100} unit="%" label="CPU" />);
    const g = screen.getByRole("img");
    expect(g).toHaveAttribute("aria-label", "CPU: 42 %");
    expect(g).toHaveAttribute("data-state", "ready");
    expect(g).toHaveAttribute("data-fraction", "0.4200");
    expect(screen.getByTestId("gauge-value")).toHaveTextContent("42");
  });

  it("rounds to the requested decimals", () => {
    render(<Gauge value={3.14159} max={10} decimals={2} unit="x" label="Pi" />);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "Pi: 3.14 x");
  });

  it("lets the caller format the number and pick the unit from the value", () => {
    render(
      <Gauge
        value={2_500_000}
        max={10_000_000}
        label="Down"
        ariaLabel="Download"
        formatValue={(v) => (v / 1e6).toFixed(1)}
        unit={(v) => (v >= 1e6 ? "Mbps" : "kbps")}
      />
    );
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "Download: 2.5 Mbps");
    expect(screen.getByTestId("gauge-value")).toHaveTextContent("2.5");
  });

  it("parks the needle at the start and shows dashes while the value is unknown", () => {
    render(<Gauge value={null} max={100} unit="%" label="RAM" />);
    const g = screen.getByRole("img");
    expect(g).toHaveAttribute("data-state", "unknown");
    expect(g).toHaveAttribute("aria-label", "RAM: no data");
    expect(screen.getByTestId("gauge-value")).toHaveTextContent("—");
    expect(screen.getByTestId("gauge-needle").style.transform).toBe("rotate(-125deg)");
  });

  it("shows the loading state instead of a reading", () => {
    render(<Gauge value={80} max={100} label="CPU" loading />);
    expect(screen.getByRole("img")).toHaveAttribute("data-state", "loading");
    expect(screen.getByTestId("gauge-value")).toHaveTextContent("—");
  });

  it("turns the needle to the value: zero is -125deg, full scale is +125deg", () => {
    const { rerender } = render(<Gauge value={0} max={100} label="a" />);
    expect(screen.getByTestId("gauge-needle").style.transform).toBe("rotate(-125deg)");
    rerender(<Gauge value={100} max={100} label="a" />);
    expect(screen.getByTestId("gauge-needle").style.transform).toBe("rotate(125deg)");
    rerender(<Gauge value={50} max={100} label="a" />);
    expect(screen.getByTestId("gauge-needle").style.transform).toBe("rotate(0deg)");
  });

  it("clamps a reading above the maximum to the end of the dial but still reports it", () => {
    render(<Gauge value={250} max={100} unit="%" label="CPU" />);
    expect(screen.getByRole("img")).toHaveAttribute("data-fraction", "1.0000");
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "CPU: 250 %");
    expect(screen.getByTestId("gauge-needle").style.transform).toBe("rotate(125deg)");
  });

  it("draws a peak marker only when given one, and mentions it in the name", () => {
    const { rerender } = render(<Gauge value={10} max={100} label="Up" />);
    expect(screen.queryByTestId("gauge-peak")).toBeNull();
    rerender(<Gauge value={10} peak={90} max={100} label="Up" />);
    expect(screen.getByTestId("gauge-peak").style.transform).toBe("rotate(100deg)");
    expect(screen.getByRole("img").getAttribute("aria-label")).toContain("peak 90");
  });

  it("labels the two ends of the scale on the full-size needle dial and drops them on the small one", () => {
    const { container, rerender } = render(<Gauge value={10} max={200} label="x" size="lg" />);
    const texts = [...container.querySelectorAll("svg text")].map((n) => n.textContent);
    expect(texts).toEqual(["0", "200"]);
    rerender(<Gauge value={10} max={200} label="x" size="sm" />);
    expect([...container.querySelectorAll("svg text")].every((n) => n.textContent === "")).toBe(true);
  });

  it("gives two gauges on the same page distinct SVG ids", () => {
    const { container } = render(
      <>
        <Gauge value={1} max={10} label="a" />
        <Gauge value={2} max={10} label="b" />
      </>
    );
    const ids = [...container.querySelectorAll("[id]")].map((n) => n.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

describe("Gauge ring style", () => {
  afterEach(() => useAppearance.getState().reset());

  it("is the default: a thick ring with the readout in the middle and no needle or ticks", () => {
    expect(DEFAULT_APPEARANCE.dial).toBe("ring");
    useAppearance.getState().reset();
    const { container } = render(<Gauge value={42} max={100} unit="%" label="CPU" />);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "CPU: 42 %");
    expect(screen.queryByTestId("gauge-needle")).toBeNull();
    expect(container.querySelectorAll("svg text")).toHaveLength(0);
    expect(screen.getByTestId("gauge-value")).toHaveTextContent("42");
  });

  it("can be forced per gauge with the dial prop, whatever the global choice", () => {
    useAppearance.getState().set({ dial: "needle" });
    render(<Gauge value={42} max={100} label="CPU" dial="ring" />);
    expect(screen.queryByTestId("gauge-needle")).toBeNull();
  });
});
