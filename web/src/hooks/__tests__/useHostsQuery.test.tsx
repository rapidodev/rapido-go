import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { PropsWithChildren } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { fetch } from "service/http";
import { useHostsQuery } from "../useHostsQuery";

vi.mock("service/http", () => ({ fetch: vi.fn() }));

afterEach(() => {
  vi.useRealTimers();
  vi.resetAllMocks();
});

const setup = () => {
  const client = new QueryClient();
  const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  let requestSignal: AbortSignal | undefined;
  vi.mocked(fetch).mockImplementation((_url, options) => {
    requestSignal = options?.signal as AbortSignal;
    return new Promise((_resolve, reject) => {
      requestSignal!.addEventListener("abort", () => reject(requestSignal!.reason), { once: true });
    }) as ReturnType<typeof fetch>;
  });
  const hook = renderHook(() => useHostsQuery(), { wrapper });
  return { ...hook, client, signal: () => requestSignal! };
};

it("times out and retries an unanswered hosts request only once", async () => {
  vi.useFakeTimers();
  const { client, signal, unmount } = setup();
  expect(signal().aborted).toBe(false);
  await act(async () => { await vi.advanceTimersByTimeAsync(8000); });
  expect(signal().aborted).toBe(true);
  expect(signal().reason.name).toBe("TimeoutError");
  await act(async () => { await vi.advanceTimersByTimeAsync(8500); });
  expect(signal().aborted).toBe(true);
  expect(vi.mocked(fetch)).toHaveBeenCalledTimes(2);
  expect(client.getQueryState(["hosts"])?.status).toBe("error");
  unmount();
  client.clear();
});

it("cancels the hosts request when leaving the page", () => {
  const { client, signal, unmount } = setup();
  expect(signal().aborted).toBe(false);
  unmount();
  expect(signal().aborted).toBe(true);
  client.clear();
});
