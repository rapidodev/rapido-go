import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { ReactElement, Suspense, lazy } from "react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { queryClient } from "utils/queryClient";
import { RouteError } from "../RouteError";
import { router } from "../Router";

const TOKEN = "test-only-session-a";

// React and jsdom both report every error a boundary catches; here they are the point.
const expectedError = (event: ErrorEvent) => event.preventDefault();

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  localStorage.setItem("token", TOKEN);
  vi.spyOn(console, "error").mockImplementation(() => undefined);
  window.addEventListener("error", expectedError);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.removeEventListener("error", expectedError);
  localStorage.clear();
  queryClient.clear();
});

const login = { path: "/login", element: <div>Login form</div> };

const renderRoute = (route: Record<string, unknown>) =>
  render(
    <RouterProvider
      router={createMemoryRouter([{ path: "/page", errorElement: <RouteError />, ...route }, { path: "/", element: <div>Overview page</div> }, login], {
        initialEntries: ["/page"],
      })}
    />
  );

describe("a route that fails", () => {
  it("stays signed in when the panel cannot be reached, and retries in place", async () => {
    const loader = vi.fn().mockRejectedValueOnce(new TypeError("Failed to fetch")).mockResolvedValue({});
    renderRoute({ loader, element: <div>The page</div> });

    expect(await screen.findByRole("alert")).toHaveTextContent("This page could not be shown");
    expect(screen.getByRole("alert")).toHaveTextContent("You are still signed in");
    expect(screen.getByRole("alert")).toHaveTextContent("Failed to fetch");
    expect(localStorage.getItem("token")).toBe(TOKEN);

    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("The page")).toBeInTheDocument();
    expect(loader).toHaveBeenCalledTimes(2);
    expect(localStorage.getItem("token")).toBe(TOKEN);
  });

  it("stays signed in when a page throws while drawing, and can leave that page", async () => {
    const Broken = (): ReactElement => {
      throw new Error("x is not a function");
    };
    renderRoute({ element: <Broken /> });

    expect(await screen.findByRole("alert")).toHaveTextContent("x is not a function");
    expect(localStorage.getItem("token")).toBe(TOKEN);

    fireEvent.click(screen.getByRole("button", { name: "Overview" }));
    expect(await screen.findByText("Overview page")).toBeInTheDocument();
    expect(localStorage.getItem("token")).toBe(TOKEN);
  });

  it("offers a reload for a chunk a deploy has replaced, and keeps the session", async () => {
    const Gone = lazy(() => Promise.reject(new TypeError("Failed to fetch dynamically imported module: https://panel.example/assets/a.js")));
    const reload = vi.fn();
    vi.stubGlobal("location", { ...window.location, reload });
    renderRoute({ element: <Suspense fallback={null}><Gone /></Suspense> });

    expect(await screen.findByRole("alert")).toHaveTextContent("This page could not be loaded");
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    expect(localStorage.getItem("token")).toBe(TOKEN);

    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(reload).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem("token")).toBe(TOKEN);
  });

  it("goes to the login page when asked to log out from there", async () => {
    renderRoute({ loader: vi.fn().mockRejectedValue(new TypeError("Failed to fetch")), element: <div>The page</div> });
    fireEvent.click(await screen.findByRole("button", { name: "Log out" }));
    expect(await screen.findByText("Login form")).toBeInTheDocument();
  });

  it("goes to the login page when the session is gone: there is nothing to retry", async () => {
    localStorage.removeItem("token");
    renderRoute({ loader: vi.fn().mockRejectedValue(Object.assign(new Error("401"), { status: 401 })), element: <div>The page</div> });
    expect(await screen.findByText("Login form")).toBeInTheDocument();
  });
});

// The real route table, end to end against a faked network. One router instance
// exists per module, so these run in order.
describe("the panel's router", () => {
  it("never uses the login form as an error page: that is what logged admins out", () => {
    const guarded = router.routes.filter((route) => route.loader);
    expect(guarded.length).toBeGreaterThan(10);
    for (const route of guarded) expect((route.errorElement as ReactElement).type, route.path).toBe(RouteError);
  });

  it("sends a visitor with no token to the login page without asking the panel", async () => {
    localStorage.removeItem("token");
    const network = vi.fn();
    vi.stubGlobal("fetch", network);
    await act(async () => {
      await router.navigate("/");
    });
    expect(router.state.location.pathname).toBe("/login");
    expect(network).not.toHaveBeenCalled();
  });

  it("keeps the session through an outage, and ends it on the real 401 that follows", async () => {
    const network = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", network);
    await act(async () => {
      await router.navigate("/users/");
    });
    render(<RouterProvider router={router} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("This page could not be shown");
    expect(localStorage.getItem("token")).toBe(TOKEN);
    expect(router.state.location.pathname).toBe("/users/");

    network.mockImplementation(async () =>
      new Response(JSON.stringify({ detail: "Could not validate credentials" }), { status: 401, headers: { "content-type": "application/json" } })
    );
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(await screen.findByRole("status")).toHaveTextContent("Your session has expired");
    expect(localStorage.getItem("token")).toBeNull();
  });
});
