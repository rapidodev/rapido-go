import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { errorText, isChunkLoadError, isUnauthorized } from "service/errors";
import { fetch as apiFetch } from "service/http";
import { queryClient } from "utils/queryClient";
import { setLoginRedirect } from "utils/session";

const TOKEN = "test-only-session-a";
const network = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>();
const redirect = vi.fn();

const answer = (status: number, body: unknown = { detail: "Could not validate credentials" }) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

beforeEach(() => {
  network.mockReset();
  redirect.mockReset();
  vi.stubGlobal("fetch", network);
  setLoginRedirect(redirect);
  localStorage.setItem("token", TOKEN);
  queryClient.setQueryData(["users", {}], { total: 1 });
});

afterEach(() => {
  vi.unstubAllGlobals();
  setLoginRedirect(() => undefined);
  localStorage.clear();
  queryClient.clear();
});

const sessionIsIntact = () => {
  expect(localStorage.getItem("token")).toBe(TOKEN);
  expect(queryClient.getQueryData(["users", {}])).toEqual({ total: 1 });
  expect(redirect).not.toHaveBeenCalled();
};

describe("a 401 from the API", () => {
  it("ends the session and goes to the login page saying it expired, and the caller still sees the error", async () => {
    network.mockImplementation(async () => answer(401));
    const failure = await apiFetch("/nodes").catch((e: unknown) => e);
    expect(isUnauthorized(failure)).toBe(true);
    expect(errorText(failure, "fallback")).toBe("Could not validate credentials");
    expect(localStorage.getItem("token")).toBeNull();
    expect(queryClient.getQueryData(["users", {}])).toBeUndefined();
    expect(redirect).toHaveBeenCalledWith(true);
  });

  it("goes to the login page once for a whole burst of them", async () => {
    network.mockImplementation(async () => answer(401));
    await Promise.allSettled([apiFetch("/nodes"), apiFetch("/users"), apiFetch("/system"), apiFetch("/monitoring")]);
    expect(redirect).toHaveBeenCalledTimes(1);
  });

  it("does not claim a session expired when there never was one", async () => {
    localStorage.removeItem("token");
    network.mockImplementation(async () => answer(401));
    await expect(apiFetch("/admin")).rejects.toBeTruthy();
    expect(redirect).toHaveBeenCalledWith(false);
  });

  it("says nothing about a newer session: a late answer to an old token's request leaves it alone", async () => {
    let finish!: (response: Response) => void;
    network.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    const request = apiFetch("/nodes");
    await vi.waitFor(() => expect(network).toHaveBeenCalled());
    localStorage.setItem("token", "test-only-session-b");
    finish(answer(401));
    await expect(request).rejects.toBeTruthy();
    expect(localStorage.getItem("token")).toBe("test-only-session-b");
    expect(redirect).not.toHaveBeenCalled();
  });
});

describe("what must not end a session", () => {
  it("is the login request's own 401 for a wrong password", async () => {
    network.mockImplementation(async () => answer(401, { detail: "Incorrect username or password" }));
    const failure = await apiFetch("/admin/token", { method: "post", body: new FormData() }).catch((e: unknown) => e);
    expect(errorText(failure, "fallback")).toBe("Incorrect username or password");
    sessionIsIntact();
  });

  it.each([400, 403, 404, 409, 422, 500, 502, 503])("is a %i", async (status) => {
    network.mockImplementation(async () => answer(status, { detail: "no" }));
    await expect(apiFetch("/nodes", { retry: 0 })).rejects.toBeTruthy();
    sessionIsIntact();
  });

  it("is a request that never got an answer", async () => {
    network.mockRejectedValue(new TypeError("Failed to fetch"));
    await expect(apiFetch("/nodes", { retry: 0 })).rejects.toBeTruthy();
    sessionIsIntact();
  });

  it("is a request that succeeded", async () => {
    network.mockImplementation(async () => answer(200, { ok: true }));
    await expect(apiFetch("/nodes")).resolves.toEqual({ ok: true });
    sessionIsIntact();
  });
});

it("sends the bearer token, and none without a session", async () => {
  network.mockImplementation(async () => answer(200, {}));
  await apiFetch("/nodes");
  expect((network.mock.calls[0][1]?.headers as Headers).get("Authorization")).toBe(`Bearer ${TOKEN}`);
  localStorage.removeItem("token");
  await apiFetch("/nodes");
  expect((network.mock.calls[1][1]?.headers as Headers).get("Authorization")).toBeNull();
});

describe("isChunkLoadError", () => {
  it.each([
    "Failed to fetch dynamically imported module: https://panel.example/dashboard/assets/UsersPage-abc.js",
    "error loading dynamically imported module: https://panel.example/dashboard/assets/UsersPage-abc.js",
    "Importing a module script failed.",
    "Unable to preload CSS for /dashboard/assets/UsersPage-abc.css",
    "'text/html' is not a valid JavaScript MIME type.",
  ])("knows %s", (message) => {
    expect(isChunkLoadError(new TypeError(message))).toBe(true);
  });

  it.each([new TypeError("Failed to fetch"), new Error("x is not a function"), null, undefined, { message: 7 }])(
    "does not mistake %s for one",
    (cause) => {
      expect(isChunkLoadError(cause)).toBe(false);
    }
  );
});
