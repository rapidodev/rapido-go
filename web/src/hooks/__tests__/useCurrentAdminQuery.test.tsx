import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor, render, screen } from "@testing-library/react";
import { PropsWithChildren } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fetch } from "service/http";
import { queryClient } from "utils/queryClient";
import { loadCurrentAdmin, loadCurrentAdminForNavigation, useCurrentAdminQuery } from "../useCurrentAdminQuery";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { SudoOnly } from "rapido-ui/SudoOnly";

vi.mock("service/http", () => ({ fetch: vi.fn() }));
const mockedFetch = vi.mocked(fetch);
const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;

beforeEach(() => { queryClient.clear(); mockedFetch.mockReset(); localStorage.setItem("token", "test-only-session-a"); });
afterEach(() => { queryClient.clear(); localStorage.clear(); vi.restoreAllMocks(); });

it("uses one identity request for navigation and mounted page hooks", async () => {
  const admin = { username: "alice", is_sudo: true };
  mockedFetch.mockResolvedValue(admin);
  await loadCurrentAdmin();
  await loadCurrentAdmin();
  const hook = renderHook(() => useCurrentAdminQuery(), { wrapper });
  await act(async () => {});
  expect(hook.result.current.data).toEqual(admin);
  expect(mockedFetch).toHaveBeenCalledTimes(1);
});

it("refreshes stale identity and does not reuse a previous session after cache clearing", async () => {
  const now = Date.now();
  const clock = vi.spyOn(Date, "now").mockReturnValue(now);
  mockedFetch.mockResolvedValueOnce({ username: "alice", is_sudo: true });
  await loadCurrentAdmin();
  clock.mockReturnValue(now + 16_000);
  mockedFetch.mockResolvedValueOnce({ username: "alice", is_sudo: false });
  expect((await loadCurrentAdmin()).is_sudo).toBe(false);
  queryClient.clear();
  mockedFetch.mockResolvedValueOnce({ username: "bob", is_sudo: false });
  expect((await loadCurrentAdmin()).username).toBe("bob");
  expect(mockedFetch).toHaveBeenCalledTimes(3);
});

it("surfaces authentication failure without a retry delay on navigation", async () => {
  mockedFetch.mockRejectedValue(new Error("unauthorized"));
  await expect(loadCurrentAdmin()).rejects.toThrow("unauthorized");
  expect(mockedFetch).toHaveBeenCalledTimes(1);
});

it("opens a cached session immediately while a stale identity refresh is pending", async () => {
  const now = Date.now();
  const clock = vi.spyOn(Date, "now").mockReturnValue(now);
  mockedFetch.mockResolvedValueOnce({ username: "alice", is_sudo: true });
  await loadCurrentAdmin();
  clock.mockReturnValue(now + 16_000);
  let finish!: (value: unknown) => void;
  mockedFetch.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }) as ReturnType<typeof fetch>);
  expect((await loadCurrentAdminForNavigation()).username).toBe("alice");
  expect((await loadCurrentAdminForNavigation()).username).toBe("alice");
  expect(mockedFetch).toHaveBeenCalledTimes(2);
  finish({ username: "alice", is_sudo: false });
  await waitFor(() => expect(queryClient.getQueryData<{ is_sudo: boolean }>(["current-admin"])?.is_sudo).toBe(false));
});

it("hides sudo content when a background identity refresh fails", async () => {
  const now = Date.now();
  const clock = vi.spyOn(Date, "now").mockReturnValue(now);
  mockedFetch.mockResolvedValueOnce({ username: "alice", is_sudo: true });
  await loadCurrentAdmin();
  render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={["/restricted"]}><Routes>
    <Route path="/restricted" element={<SudoOnly><div>Private page</div></SudoOnly>} />
    <Route path="/" element={<div>Public page</div>} />
  </Routes></MemoryRouter></QueryClientProvider>);
  expect(screen.getByText("Private page")).toBeInTheDocument();
  clock.mockReturnValue(now + 16_000);
  mockedFetch.mockRejectedValue(new Error("unauthorized"));
  await act(async () => { await loadCurrentAdminForNavigation(); });
  await waitFor(() => expect(screen.getByText("Public page")).toBeInTheDocument());
  expect(screen.queryByText("Private page")).not.toBeInTheDocument();
  await expect(loadCurrentAdminForNavigation()).rejects.toThrow("unauthorized");
});

it("clears the previous account's cached data when the session changes", async () => {
  mockedFetch.mockResolvedValueOnce({ username: "alice", is_sudo: true });
  await loadCurrentAdmin();
  queryClient.setQueryData(["users", {}], { owner: "test-only-account-a" });
  localStorage.setItem("token", "test-only-session-b");
  mockedFetch.mockResolvedValueOnce({ username: "bob", is_sudo: false });
  expect((await loadCurrentAdminForNavigation()).username).toBe("bob");
  expect(queryClient.getQueryData(["users", {}])).toBeUndefined();
});

it("does not reuse an identity after the token has been removed", async () => {
  mockedFetch.mockResolvedValueOnce({ username: "alice", is_sudo: true });
  await loadCurrentAdmin();
  localStorage.removeItem("token");
  mockedFetch.mockRejectedValue(new Error("unauthorized"));
  await expect(loadCurrentAdminForNavigation()).rejects.toThrow("unauthorized");
  expect(queryClient.getQueryData(["current-admin"])).toBeUndefined();
});

it("rejects an identity response that arrives after the session changed", async () => {
  let finish!: (value: unknown) => void;
  mockedFetch.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }) as ReturnType<typeof fetch>);
  const request = loadCurrentAdminForNavigation();
  localStorage.setItem("token", "test-only-session-b");
  finish({ username: "alice", is_sudo: true });
  await expect(request).rejects.toThrow("Session changed");
  expect(queryClient.getQueryData(["current-admin"])).toBeUndefined();
});
