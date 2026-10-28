import { afterEach, describe, expect, it, vi } from "vitest";
import { createPagePrefetcher } from "../pageImports";

afterEach(() => vi.unstubAllGlobals());

describe("page chunk prefetching", () => {
  it("shares a pending download and avoids downloading the same page again", async () => {
    let complete!: () => void;
    const load = vi.fn(() => new Promise<void>((resolve) => { complete = resolve; }));
    const prefetch = createPagePrefetcher({ "/users/": load });
    const first = prefetch("/users/");
    const second = prefetch("/users/");
    expect(load).toHaveBeenCalledTimes(1);
    complete();
    await Promise.all([first, second]);
    await prefetch("/users/");
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("permits retry after a speculative request fails", async () => {
    const load = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce({});
    const prefetch = createPagePrefetcher({ "/users/": load });
    await prefetch("/users/");
    await prefetch("/users/");
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("does not prefetch unknown routes or on a data-saving connection", async () => {
    const load = vi.fn().mockResolvedValue({});
    const prefetch = createPagePrefetcher({ "/users/": load });
    await prefetch("/unregistered/");
    vi.stubGlobal("navigator", { connection: { saveData: true } });
    await prefetch("/users/");
    expect(load).not.toHaveBeenCalled();
  });
});
