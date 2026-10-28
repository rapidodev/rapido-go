import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { PropsWithChildren } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fetch } from "service/http";
import { queryClient } from "utils/queryClient";
import { loadCurrentAdmin, useCurrentAdminQuery } from "../useCurrentAdminQuery";

vi.mock("service/http", () => ({ fetch: vi.fn() }));
const mockedFetch = vi.mocked(fetch);
const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;

beforeEach(() => { queryClient.clear(); mockedFetch.mockReset(); });
afterEach(() => { queryClient.clear(); vi.restoreAllMocks(); });

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
