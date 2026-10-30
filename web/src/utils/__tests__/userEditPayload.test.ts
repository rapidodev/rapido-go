import { describe, expect, it } from "vitest";
import { Status, User } from "types/User";
import { GIB, DAY_SECONDS } from "utils/userConversions";
import {
  buildUserEdit,
  onHoldDaysMissing,
  userEditValues,
  userStatusOptions,
} from "utils/userEditPayload";

const NOW = 1_800_000_000_000;

const user = (over: Partial<User> = {}): User =>
  ({
    username: "alice",
    status: "active",
    data_limit: 10 * GIB,
    data_limit_reset_strategy: "month",
    expire: Math.floor(NOW / 1000) + 30 * DAY_SECONDS + 1234,
    note: "vip",
    on_hold_expire_duration: null,
    proxies: { vless: { id: "the-credential", flow: "xtls-rprx-vision" } },
    inbounds: { vless: ["a", "b"] },
    ...over,
  }) as User;

const edit = (
  u: User,
  change: Partial<ReturnType<typeof userEditValues>> = {},
  protocols: string[] = Object.keys(u.proxies),
  inbounds: Record<string, string[]> = u.inbounds,
  touched: string[] = Object.keys(u.inbounds)
) =>
  buildUserEdit(
    u,
    { ...userEditValues(u, NOW), ...change },
    new Set(protocols),
    Object.fromEntries(Object.entries(inbounds).map(([k, v]) => [k, new Set(v)])),
    new Set(touched),
    NOW
  );

describe("buildUserEdit", () => {
  it("sends nothing when nothing changed", () => {
    expect(edit(user())).toEqual({});
  });

  it("never touches a protocol the user keeps, so its credential stays", () => {
    const body = edit(user(), { note: "changed" });
    expect(body).toEqual({ note: "changed" });
    expect(body.proxies).toBeUndefined();
  });

  it("asks for a credential only for a protocol newly ticked", () => {
    const body = edit(user(), {}, ["vless", "trojan"]);
    expect(body.proxies).toEqual({ trojan: {} });
  });

  it("removes a protocol unticked with an explicit null", () => {
    const u = user({ proxies: { vless: {}, trojan: {} } });
    expect(edit(u, {}, ["trojan"]).proxies).toEqual({ vless: null });
  });

  it("does not move the expiry when the days field wasn't touched", () => {
    expect(edit(user(), { note: "x" }).expire).toBeUndefined();
  });

  it("sets the expiry when the days field changed", () => {
    expect(edit(user(), { expire_days: "7" }).expire).toBe(Math.floor(NOW / 1000) + 7 * DAY_SECONDS);
  });

  it("clears a limit and an expiry with 0, since null would mean unchanged", () => {
    const body = edit(user(), { data_limit_gb: "", expire_days: "" });
    expect(body.data_limit).toBe(0);
    expect(body.expire).toBe(0);
  });

  it("drops the reset schedule along with the limit", () => {
    expect(edit(user(), { data_limit_gb: "" }).data_limit_reset_strategy).toBe("no_reset");
    expect(edit(user(), { data_limit_gb: "0" }).data_limit_reset_strategy).toBe("no_reset");
  });

  it("keeps a schedule while there is a limit", () => {
    expect(edit(user(), { data_limit_reset_strategy: "week" }).data_limit_reset_strategy).toBe("week");
    expect(edit(user(), { data_limit_gb: "5" }).data_limit_reset_strategy).toBeUndefined();
  });

  it.each<Status>(["limited", "expired"])("saves a %s user without sending that status back", (status) => {
    expect(edit(user({ status }), { note: "x" })).toEqual({ note: "x" });
  });

  it("saves an on-hold user without its status, expiry or duration", () => {
    const u = user({ status: "on_hold", expire: null, on_hold_expire_duration: 5 * DAY_SECONDS });
    expect(edit(u, { note: "x" })).toEqual({ note: "x" });
  });

  it("changes an on-hold user's duration alone", () => {
    const u = user({ status: "on_hold", expire: null, on_hold_expire_duration: 5 * DAY_SECONDS });
    expect(edit(u, { on_hold_days: "9" })).toEqual({ on_hold_expire_duration: 9 * DAY_SECONDS });
  });

  it("moving to on hold drops the expiry, which the API refuses alongside it", () => {
    expect(edit(user(), { status: "on_hold", on_hold_days: "3" })).toEqual({
      status: "on_hold",
      expire: 0,
      on_hold_expire_duration: 3 * DAY_SECONDS,
    });
  });

  it("sends an inbound list only when it differs from what the user has", () => {
    expect(edit(user(), {}, ["vless"], { vless: ["b", "a"] }).inbounds).toBeUndefined();
    expect(edit(user(), {}, ["vless"], { vless: ["a"] }).inbounds).toEqual({ vless: ["a"] });
  });

  it("leaves an inbound list nobody touched to the server's default", () => {
    expect(edit(user(), {}, ["vless"], { vless: ["a"] }, []).inbounds).toBeUndefined();
  });

  it("shows an expired user's negative days and doesn't resend them", () => {
    const u = user({ status: "expired", expire: Math.floor(NOW / 1000) - 3 * DAY_SECONDS });
    expect(userEditValues(u, NOW).expire_days).toBe("-3");
    expect(edit(u)).toEqual({});
  });
});

describe("userStatusOptions", () => {
  it("offers what the API accepts, and the current status", () => {
    expect(userStatusOptions("limited")).toEqual(["limited", "active", "on_hold", "disabled"]);
    expect(userStatusOptions("active")).toEqual(["active", "on_hold", "disabled"]);
  });
});

describe("onHoldDaysMissing", () => {
  it("wants a duration only for on hold", () => {
    const values = userEditValues(user(), NOW);
    expect(onHoldDaysMissing(values)).toBe(false);
    expect(onHoldDaysMissing({ ...values, status: "on_hold", on_hold_days: "" })).toBe(true);
    expect(onHoldDaysMissing({ ...values, status: "on_hold", on_hold_days: "0" })).toBe(true);
    expect(onHoldDaysMissing({ ...values, status: "on_hold", on_hold_days: "2" })).toBe(false);
  });
});
