import { afterEach, describe, expect, it, vi } from "vitest";
import { User } from "types/User";
import { pushSample } from "rapido-ui/useRollingSeries";
import { useUsersUiStore } from "rapido-ui/usersUiStore";
import { queryClient } from "utils/queryClient";
import { clearSession, endSession, setLoginRedirect } from "utils/session";

afterEach(() => {
  localStorage.clear();
  setLoginRedirect(() => undefined);
});

describe("clearSession", () => {
  it("drops the token and the query cache", () => {
    localStorage.setItem("token", "test-only-session");
    queryClient.setQueryData(["users", {}], { total: 1 });
    clearSession();
    expect(localStorage.getItem("token")).toBeNull();
    expect(queryClient.getQueryData(["users", {}])).toBeUndefined();
  });

  it("leaves none of one admin's filters, selection or dialogs for the next", () => {
    const someone = { username: "customer-of-a" } as User;
    const ui = useUsersUiStore.getState();
    ui.setFilters({ search: "customer-of-a", admin: "reseller-a", status: "disabled", offset: 40 });
    ui.setEditingUser(someone);
    ui.setDeletingUser(someone);
    ui.setResetUsageUser(someone);
    ui.setRevokeSubscriptionUser(someone);
    ui.setSubscriptionLinkUser(someone);
    ui.setCreatingNewUser(true);

    clearSession();

    const after = useUsersUiStore.getState();
    expect(after.filters).toEqual({ sort: "-created_at", limit: 10 });
    expect(after.editingUser).toBeNull();
    expect(after.deletingUser).toBeNull();
    expect(after.resetUsageUser).toBeNull();
    expect(after.revokeSubscriptionUser).toBeNull();
    expect(after.subscriptionLinkUser).toBeNull();
    expect(after.isCreatingNewUser).toBe(false);
    // The actions survive the reset: the store still works for the next admin.
    after.setFilters({ search: "next" });
    expect(useUsersUiStore.getState().filters.search).toBe("next");
    clearSession();
  });

  it("keeps the page size, a preference of this browser and not of the admin", () => {
    localStorage.setItem("rapido-num-users-per-page", "50");
    clearSession();
    expect(useUsersUiStore.getState().filters.limit).toBe(50);
    localStorage.clear();
    clearSession();
  });

  it("starts the rolling series over", () => {
    pushSample("overview:online", 120, "a");
    expect(pushSample("overview:online", 121, "b")).toEqual([120, 121]);
    clearSession();
    expect(pushSample("overview:online", 5, "c")).toEqual([5]);
    clearSession();
  });
});

describe("endSession", () => {
  it("clears the session, then goes to the login page saying whether it expired", () => {
    const redirect = vi.fn(() => expect(localStorage.getItem("token")).toBeNull());
    setLoginRedirect(redirect);
    localStorage.setItem("token", "test-only-session");
    endSession(true);
    expect(redirect).toHaveBeenCalledWith(true);
  });
});
