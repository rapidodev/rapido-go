import { act, fireEvent, render, screen } from "@testing-library/react";
import dayjs from "dayjs";
import Duration from "dayjs/plugin/duration";
import utc from "dayjs/plugin/utc";
import i18n from "locales/i18n";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { User } from "types/User";

const state = vi.hoisted(() => ({
  users: [] as unknown[],
  sudo: false,
  admins: [] as { username: string }[],
}));

vi.mock("hooks/useUsersQuery", () => ({
  useUsersQuery: () => ({ data: { users: state.users, total: state.users.length }, isLoading: false }),
}));

// The reseller filter needs the role and the admin list; neither is under test
// here, so they are stubbed instead of standing up a QueryClient.
vi.mock("hooks/useCurrentAdminQuery", () => ({ useIsSudo: () => state.sudo }));
vi.mock("hooks/useAdminsQuery", () => ({ useAdminsQuery: () => ({ data: state.admins }) }));

import { UsersTable } from "../UsersTable";
import { useUsersUiStore } from "../usersUiStore";

// main.tsx registers these; the "last seen" text needs them.
dayjs.extend(utc);
dayjs.extend(Duration);

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

afterEach(() => {
  vi.useRealTimers();
});

const NOW = Date.parse("2026-09-25T12:00:00Z");
const ago = (seconds: number) => new Date(NOW - seconds * 1000).toISOString();

const user = (o: Partial<User>): User =>
  ({
    id: 1,
    username: "alice",
    status: "active",
    used_traffic: 0,
    lifetime_used_traffic: 0,
    data_limit: null,
    expire: null,
    proxies: {},
    online_at: null,
    synced_from_panel_name: null,
    ...o,
  } as unknown as User);

const renderUser = (o: Partial<User>) => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  state.users = [user(o)];
  let view!: ReturnType<typeof render>;
  act(() => {
    view = render(<UsersTable />);
  });
  return view;
};

const presenceDotPings = (container: HTMLElement) => container.querySelector(".animate-ping") !== null;

describe("Users table presence", () => {
  it("shows a connected user as online from the live flag even when online_at is old", () => {
    const { container } = renderUser({ online: true, online_at: ago(3600) });
    expect(screen.getByText("Online now")).toBeInTheDocument();
    expect(presenceDotPings(container)).toBe(true);
  });

  it("shows a user with recent traffic but no open connection as last seen, not online", () => {
    const { container } = renderUser({ online: false, online_at: ago(90) });
    expect(screen.queryByText("Online now")).not.toBeInTheDocument();
    expect(screen.getByText(/last seen .*1 min.* ago/)).toBeInTheDocument();
    expect(presenceDotPings(container)).toBe(false);
  });

  it("does not print a blank time for activity under a minute ago", () => {
    renderUser({ online: false, online_at: ago(20) });
    expect(screen.getByText(/last seen .*under a minute.* ago/)).toBeInTheDocument();
  });

  it("falls back to the 180 s online_at rule only when the backend sends no flag", () => {
    const fresh = renderUser({ online_at: ago(60) });
    expect(screen.getByText("Online now")).toBeInTheDocument();
    fresh.unmount();

    renderUser({ online_at: ago(600) });
    expect(screen.queryByText("Online now")).not.toBeInTheDocument();
    expect(screen.getByText(/last seen .*10 mins.* ago/)).toBeInTheDocument();
  });

  it("says never connected when there is no activity and the user is not online", () => {
    renderUser({ online: false, online_at: null });
    expect(screen.getByText("Never connected")).toBeInTheDocument();
  });
});

describe("Users table filters", () => {
  afterEach(() => {
    state.sudo = false;
    state.admins = [];
    act(() => useUsersUiStore.getState().setFilters({ admin: undefined }));
  });

  it("lets the search box take a config UID, not just a username", () => {
    render(<UsersTable />);
    expect(screen.getByPlaceholderText(/config UID/i)).toBeInTheDocument();
  });

  it("offers a reseller filter to the sudo admin, listing every admin", () => {
    state.sudo = true;
    state.admins = [{ username: "reseller-a" }, { username: "reseller-b" }];
    render(<UsersTable />);
    const select = screen.getByLabelText("Filter by reseller") as HTMLSelectElement;
    expect(Array.from(select.options).map((o) => o.text)).toEqual(["All resellers", "reseller-a", "reseller-b"]);
  });

  it("filters the list to one reseller's configs and back to everyone", () => {
    state.sudo = true;
    state.admins = [{ username: "reseller-a" }];
    render(<UsersTable />);
    const select = screen.getByLabelText("Filter by reseller");
    fireEvent.change(select, { target: { value: "reseller-a" } });
    expect(useUsersUiStore.getState().filters.admin).toBe("reseller-a");
    fireEvent.change(select, { target: { value: "" } });
    expect(useUsersUiStore.getState().filters.admin).toBeUndefined();
  });

  it("hides the reseller filter from a reseller, who only ever sees their own configs", () => {
    state.sudo = false;
    state.admins = [{ username: "reseller-a" }];
    render(<UsersTable />);
    expect(screen.queryByLabelText("Filter by reseller")).not.toBeInTheDocument();
  });
});
