import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const identity = vi.hoisted(() => {
  const proven = { data: { username: "a", is_sudo: true }, isPending: false } as { data?: { username: string; is_sudo: boolean }; isPending: boolean };
  return { proven, query: proven };
});
vi.mock("hooks/useCurrentAdminQuery", () => ({
  useCurrentAdminQuery: () => identity.query,
}));

import { queryClient } from "utils/queryClient";
import { RapidoShell } from "../Shell";
import { useUsersUiStore } from "../usersUiStore";

const original = window.matchMedia;
const setViewport = (desktop: boolean) => {
  window.matchMedia = ((q: string) => ({
    matches: q.includes("min-width: 768px") ? desktop : false,
    media: q,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  })) as unknown as typeof window.matchMedia;
};

const renderShell = () =>
  render(
    <MemoryRouter>
      <RapidoShell active="overview">
        <p>page</p>
      </RapidoShell>
    </MemoryRouter>
  );

beforeEach(() => {
  window.localStorage.clear();
  document.body.style.overflow = "";
  identity.query = identity.proven;
});
afterEach(() => {
  window.matchMedia = original;
  document.body.style.overflow = "";
});

describe("what the shell knows about the admin", () => {
  beforeEach(() => setViewport(true));

  it("keeps the sudo links through a failed refresh of a role it already knows", () => {
    // What react-query reports then: the last answer is still there, status is "error".
    identity.query = { data: { username: "a", is_sudo: true }, isPending: false };
    renderShell();
    expect(screen.getByRole("link", { name: "Admins" })).toBeInTheDocument();
  });

  it("shows no sudo link until the role is known", () => {
    identity.query = { data: undefined, isPending: true };
    renderShell();
    expect(screen.queryByRole("link", { name: "Admins" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Users" })).toBeInTheDocument();
  });

  it("logging out ends the session in this browser", () => {
    window.localStorage.setItem("token", "test-only-session");
    queryClient.setQueryData(["users", {}], { total: 1 });
    useUsersUiStore.getState().setFilters({ search: "someone" });
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Log out" }));
    expect(window.localStorage.getItem("token")).toBeNull();
    expect(queryClient.getQueryData(["users", {}])).toBeUndefined();
    expect(useUsersUiStore.getState().filters.search).toBeUndefined();
  });
});

describe("phone menu drawer", () => {
  beforeEach(() => setViewport(false));

  it("is closed to begin with: not in the tab order, hidden from assistive tech, menu button collapsed", () => {
    renderShell();
    const nav = document.getElementById("rapido-nav") as HTMLElement;
    expect(nav).toHaveAttribute("aria-hidden", "true");
    expect(nav).toHaveAttribute("inert");
    expect(nav.className).toContain("-translate-x-[115%]");
    expect(screen.getByRole("button", { name: "Open menu" })).toHaveAttribute("aria-expanded", "false");
  });

  it("opens from the menu button as a modal dialog, locks body scroll and moves focus in", () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open menu" }));
    const nav = document.getElementById("rapido-nav") as HTMLElement;
    expect(nav).toHaveAttribute("role", "dialog");
    expect(nav).toHaveAttribute("aria-modal", "true");
    expect(nav).not.toHaveAttribute("inert");
    expect(nav.className).toContain("max-md:translate-x-0");
    expect(document.body.style.overflow).toBe("hidden");
    expect(nav.contains(document.activeElement)).toBe(true);
    expect(screen.getByRole("button", { name: "Open menu" })).toHaveAttribute("aria-expanded", "true");
  });

  it("closes on Escape, hands focus back to the menu button and releases the scroll lock", () => {
    renderShell();
    const opener = screen.getByRole("button", { name: "Open menu" });
    fireEvent.click(opener);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(document.getElementById("rapido-nav")).toHaveAttribute("aria-hidden", "true");
    expect(document.body.style.overflow).toBe("");
    expect(document.activeElement).toBe(opener);
  });

  it("closes when the dimmed backdrop is tapped", () => {
    const { container } = renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open menu" }));
    fireEvent.click(container.querySelector(".rp-drawer-backdrop") as HTMLElement);
    expect(document.getElementById("rapido-nav")).toHaveAttribute("aria-hidden", "true");
  });

  it("closes when a section is chosen", () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open menu" }));
    fireEvent.click(screen.getAllByRole("link", { name: /Users/ })[0]);
    expect(document.getElementById("rapido-nav")).toHaveAttribute("aria-hidden", "true");
  });

  it("closes on a swipe toward the start edge, in the page direction", () => {
    renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Open menu" }));
    const nav = document.getElementById("rapido-nav") as HTMLElement;
    fireEvent.touchStart(nav, { touches: [{ clientX: 200, clientY: 100 }] });
    fireEvent.touchMove(nav, { touches: [{ clientX: 80, clientY: 104 }] });
    expect(nav).toHaveAttribute("aria-hidden", "true");
  });
});

describe("desktop rail", () => {
  beforeEach(() => setViewport(true));

  it("is a visible rail with no dialog semantics and no menu button needed", () => {
    renderShell();
    const nav = document.getElementById("rapido-nav") as HTMLElement;
    expect(nav).not.toHaveAttribute("aria-hidden");
    expect(nav).not.toHaveAttribute("role");
  });

  it("collapses to icons only, keeps the labels for screen readers, and remembers the choice", () => {
    const first = renderShell();
    fireEvent.click(screen.getByRole("button", { name: "Collapse menu" }));
    const nav = document.getElementById("rapido-nav") as HTMLElement;
    expect(nav.className).toContain("md:w-[4.75rem]");
    expect(screen.getByRole("link", { name: "Users" }).querySelector("span")).toHaveClass("md:sr-only");
    expect(window.localStorage.getItem("rapido.navCollapsed")).toBe("1");
    first.unmount();
    renderShell();
    expect((document.getElementById("rapido-nav") as HTMLElement).className).toContain("md:w-[4.75rem]");
    fireEvent.click(screen.getAllByRole("button", { name: "Expand menu" })[0]);
    expect(window.localStorage.getItem("rapido.navCollapsed")).toBe("0");
  });
});
