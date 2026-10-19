import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

vi.mock("hooks/useCurrentAdminQuery", () => ({
  useCurrentAdminQuery: () => ({ data: { username: "a", is_sudo: true }, isSuccess: true, isPending: false }),
}));

import { RapidoShell } from "../Shell";

// The 3D scene and card tilt move things with transforms, which can make the page scroll
// sideways if anything reaches past the viewport. These pin the containment that prevents it.
describe("horizontal overflow guard", () => {
  it("clips the shell root sideways and lets it shrink", () => {
    const { container } = render(
      <MemoryRouter>
        <RapidoShell active="overview">
          <p>page</p>
        </RapidoShell>
      </MemoryRouter>
    );
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).toContain("overflow-x-clip");
    expect(root.className).toContain("max-w-full");
  });

  const css = readFileSync(resolve(__dirname, "../tailwind.css"), "utf8");
  const block = (selector: string) => {
    const at = css.indexOf(selector);
    return at < 0 ? "" : css.slice(at, css.indexOf("}", at));
  };

  it("hides sideways overflow on the page, with clip where supported", () => {
    expect(block("html {")).toMatch(/overflow-x:\s*hidden/);
    expect(css).toMatch(/@supports \(overflow:\s*clip\)[\s\S]*?overflow-x:\s*clip/);
  });

  it("contains the 3D scene so it cannot add scroll width", () => {
    const world = block(".rp-world {");
    expect(world).toMatch(/overflow:\s*clip/);
    expect(world).toMatch(/contain:\s*strict/);
    expect(world).toMatch(/pointer-events:\s*none/);
  });
});
