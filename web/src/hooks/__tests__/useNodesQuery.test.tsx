import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { PropsWithChildren } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { fetch } from "service/http";
import { useNodesQuery } from "../useNodesQuery";

vi.mock("service/http", () => ({ fetch: vi.fn() }));

afterEach(() => {
  vi.resetAllMocks();
});

// The hook's poll decision as the query cache holds it.
type PollFn = (query: unknown) => number | false;
const pollOf = (options: unknown) => (options as { refetchInterval: PollFn }).refetchInterval;

const render = (answer: unknown) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  vi.mocked(fetch).mockResolvedValue(answer as never);
  return { client, ...renderHook(() => useNodesQuery(), { wrapper }) };
};

// The poll decision runs inside every page that lists nodes; an answer that is
// not a list must not throw there and take the page down.
it("does not throw or poll when the answer is not a list", async () => {
  const { result, client } = render({ detail: "not a list" });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  const query = client.getQueryCache().find({ queryKey: ["nodes"] });
  expect(query).toBeDefined();
  const interval = pollOf(query!.options);
  expect(interval(query)).toBe(false);
});

it("polls while a node is connecting, and stops once none is", async () => {
  const { result, client } = render([{ id: 1, status: "connecting" }]);
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  const query = client.getQueryCache().find({ queryKey: ["nodes"] })!;
  const interval = pollOf(query.options);
  expect(interval(query)).toBe(3000);
  query.setData([{ id: 1, status: "connected" }]);
  expect(interval(query)).toBe(false);
});
