import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { OUTBOUND_TEST_BATCH, OutboundTestCell, useOutboundTests } from "../OutboundTests";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock, $fetch: fetchMock }));

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  fetchMock.mockReset();
});

const base = { node_id: 1, node: "n1", delay_ms: 0, status_code: 0, interface_here: false, fallback: false };

describe("useOutboundTests", () => {
  it("tests a long list one batch at a time", async () => {
    let inFlight = 0;
    let most = 0;
    fetchMock.mockImplementation(async (_url: string, opts: { body: { tags: string[] } }) => {
      inFlight += 1;
      most = Math.max(most, inFlight);
      await new Promise((resolve) => setTimeout(resolve, 5));
      inFlight -= 1;
      return { url: "", nodes: [], results: opts.body.tags.map((tag) => ({ ...base, tag, delay_ms: 50, status_code: 204 })) };
    });
    const { result } = renderHook(() => useOutboundTests());
    const tags = Array.from({ length: OUTBOUND_TEST_BATCH * 2 + 3 }, (_, i) => `exit-${i}`);

    await act(() => result.current.testAll(tags, 0));

    const sizes = fetchMock.mock.calls.map(([, opts]) => (opts.body as { tags: string[] }).tags.length);
    expect(sizes).toEqual([OUTBOUND_TEST_BATCH, OUTBOUND_TEST_BATCH, 3]);
    expect(most).toBe(1);
    expect(fetchMock.mock.calls[0][1]).toEqual({ method: "POST", body: { tags: tags.slice(0, OUTBOUND_TEST_BATCH) } });
    expect(result.current.testingAll).toBe(false);
    for (const tag of tags) {
      expect(result.current.byTag[tag]).toEqual({ running: false, results: [{ ...base, tag, delay_ms: 50, status_code: 204 }] });
    }
  });

  it("marks every outbound as running until its batch answers", async () => {
    let answer: (value: unknown) => void = () => undefined;
    fetchMock.mockImplementation(() => new Promise((resolve) => (answer = resolve)));
    const { result } = renderHook(() => useOutboundTests());
    let done: Promise<void> = Promise.resolve();
    act(() => {
      done = result.current.test(["exit"], 3);
    });
    expect(result.current.byTag.exit).toEqual({ running: true });
    expect(fetchMock.mock.calls[0][1]).toEqual({ method: "POST", body: { tags: ["exit"], node_id: 3 } });
    await act(async () => {
      answer({ url: "", nodes: [], results: [] });
      await done;
    });
    expect(result.current.byTag.exit).toEqual({ running: false, results: [] });
  });
});

describe("OutboundTestCell", () => {
  it("shows nothing before a test", () => {
    render(<OutboundTestCell />);
    expect(screen.getByText("-")).toBeInTheDocument();
  });

  it("says what could not be tested rather than showing an error", () => {
    render(
      <OutboundTestCell
        state={{
          running: false,
          results: [
            { ...base, tag: "b", skipped: "block" },
            { ...base, node_id: 2, node: "n2", tag: "b", skipped: "missing" },
          ],
        }}
      />
    );
    expect(screen.getByText("block: nothing to test")).toBeInTheDocument();
    expect(screen.getByText("not on this node")).toBeInTheDocument();
    expect(screen.getByText("n2")).toBeInTheDocument();
  });

  it("names the missing tunnel when a pinned exit fails on a node without it", () => {
    render(
      <OutboundTestCell
        state={{ running: false, results: [{ ...base, tag: "x", interface: "wg-de", error: "bind: no such device" }] }}
      />
    );
    const text = screen.getByText("tunnel wg-de is not on this node");
    expect(text).toHaveAttribute("title", "bind: no such device");
  });

  it("shows a status other than 2xx as a warning, with the delay", async () => {
    render(<OutboundTestCell state={{ running: false, results: [{ ...base, tag: "x", delay_ms: 310, status_code: 403 }] }} />);
    await waitFor(() => expect(screen.getByText("310ms (403)")).toHaveClass("text-yellow-300"));
  });
});
