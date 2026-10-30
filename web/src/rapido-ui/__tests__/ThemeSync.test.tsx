import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adoptNewSession, loadCurrentAdminForNavigation } from "hooks/useCurrentAdminQuery";
import { removeAuthToken, setAuthToken } from "utils/authStorage";
import { queryClient } from "utils/queryClient";
import { setPanelDefault } from "../appearance";
import { usePanelTheme } from "../panelTheme";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { ThemeSync } from "../ThemeSync";

const themeRequests = () => fetchMock.mock.calls.filter((c) => c[0] === "/settings/theme").length;

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockImplementation((url: string) =>
    Promise.resolve(url === "/settings/theme" ? { theme: { preset: "aurora" } } : { username: "alice", is_sudo: true })
  );
});

afterEach(() => {
  localStorage.clear();
  queryClient.clear();
  setPanelDefault(null);
  usePanelTheme.getState().set(null);
});

const mount = () =>
  render(
    <QueryClientProvider client={queryClient}>
      <ThemeSync />
    </QueryClientProvider>
  );

describe("ThemeSync", () => {
  it("does not ask before anyone is signed in", async () => {
    mount();
    await act(async () => {});
    expect(themeRequests()).toBe(0);
  });

  it("asks right away for an admin already signed in", async () => {
    localStorage.setItem("token", "test-only-session");
    mount();
    await waitFor(() => expect(usePanelTheme.getState().theme).toEqual({ preset: "aurora" }));
  });

  it("asks as soon as this tab signs in: the login's navigation fires no hashchange", async () => {
    mount();
    await act(async () => {
      setAuthToken("test-only-session");
    });
    await waitFor(() => expect(usePanelTheme.getState().theme).toEqual({ preset: "aurora" }));
    expect(themeRequests()).toBe(1);
  });

  it("is not cancelled by the navigation the login goes on to make", async () => {
    let answerTheme!: (value: unknown) => void;
    fetchMock.mockImplementation((url: string) =>
      url === "/settings/theme"
        ? new Promise((resolve) => {
            answerTheme = resolve;
          })
        : Promise.resolve({ username: "alice", is_sudo: true })
    );
    mount();
    await act(async () => {
      setAuthToken("test-only-session");
      adoptNewSession();
    });
    await waitFor(() => expect(themeRequests()).toBe(1));

    // The request is in flight when the route's loader runs.
    await act(async () => {
      await loadCurrentAdminForNavigation();
    });
    answerTheme({ theme: { preset: "aurora" } });
    await waitFor(() => expect(usePanelTheme.getState().theme).toEqual({ preset: "aurora" }));
  });

  it("hears another tab signing in through the storage event", async () => {
    mount();
    localStorage.setItem("token", "test-only-session");
    await act(async () => {
      window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    });
    await waitFor(() => expect(themeRequests()).toBe(1));
  });

  it("does not ask again once signed out", async () => {
    localStorage.setItem("token", "test-only-session");
    mount();
    await waitFor(() => expect(themeRequests()).toBe(1));
    await act(async () => {
      removeAuthToken();
      queryClient.clear();
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(themeRequests()).toBe(1);
  });
});
