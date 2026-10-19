import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Card } from "../Card";
import { tiltAngles } from "../useTilt";

describe("tiltAngles", () => {
  it("is flat at the centre and tips toward the pointer at the edges", () => {
    expect(tiltAngles(0.5, 0.5, 4)).toEqual({ rx: 0, ry: 0 });
    expect(tiltAngles(1, 0.5, 4).ry).toBe(4);
    expect(tiltAngles(0, 0.5, 4).ry).toBe(-4);
    expect(tiltAngles(0.5, 0, 4).rx).toBe(4);
    expect(tiltAngles(0.5, 1, 4).rx).toBe(-4);
  });

  it("never exceeds the maximum, even for a pointer outside the box", () => {
    expect(tiltAngles(9, -9, 3)).toEqual({ rx: 3, ry: 3 });
  });
});

// jsdom has no PointerEvent, so a pointermove is a MouseEvent with that type.
const move = (el: HTMLElement, x: number, y: number, pointerType?: string) => {
  const ev = new MouseEvent("pointermove", { bubbles: true, clientX: x, clientY: y });
  if (pointerType) Object.defineProperty(ev, "pointerType", { value: pointerType });
  fireEvent(el, ev);
};

describe("Card tilt", () => {
  const original = window.matchMedia;
  beforeEach(() => {
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      cb(0);
      return 1;
    });
    window.matchMedia = ((q: string) => ({
      matches: false,
      media: q,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
    })) as unknown as typeof window.matchMedia;
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    window.matchMedia = original;
  });

  const box = (el: HTMLElement) =>
    vi.spyOn(el, "getBoundingClientRect").mockReturnValue({
      left: 0, top: 0, right: 200, bottom: 100, width: 200, height: 100, x: 0, y: 0, toJSON() {},
    });

  it("writes the tilt as CSS variables and clears them when the pointer leaves", () => {
    render(<Card data-testid="c" tilt>x</Card>);
    const el = screen.getByTestId("c");
    box(el);
    move(el, 200, 0);
    expect(el.style.getPropertyValue("--rp-ry")).toBe("4.00deg");
    expect(el.style.getPropertyValue("--rp-rx")).toBe("4.00deg");
    fireEvent.pointerLeave(el);
    expect(el.style.getPropertyValue("--rp-ry")).toBe("");
  });

  it("does nothing for touch input or when tilt is off", () => {
    render(<><Card data-testid="a" tilt>x</Card><Card data-testid="b">y</Card></>);
    const a = screen.getByTestId("a");
    const b = screen.getByTestId("b");
    box(a);
    box(b);
    move(a, 200, 0, "touch");
    move(b, 200, 0);
    expect(a.style.getPropertyValue("--rp-ry")).toBe("");
    expect(b.style.getPropertyValue("--rp-ry")).toBe("");
    expect(b).not.toHaveClass("rp-tilt");
  });

  it("does not tilt under prefers-reduced-motion", () => {
    window.matchMedia = ((q: string) => ({
      matches: true,
      media: q,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
    })) as unknown as typeof window.matchMedia;
    render(<Card data-testid="c" tilt>x</Card>);
    const el = screen.getByTestId("c");
    box(el);
    move(el, 200, 0);
    expect(el.style.getPropertyValue("--rp-ry")).toBe("");
  });
});
