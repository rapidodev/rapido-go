import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import dayjs from "dayjs";
import Duration from "dayjs/plugin/duration";
import utc from "dayjs/plugin/utc";
import i18n from "locales/i18n";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { User } from "types/User";

const state = vi.hoisted(() => ({
  users: [] as unknown[],
  total: null as number | null,
  sudo: false,
  admins: [] as { username: string }[],
  failed: null as unknown,
  placeholder: false,
  refetch: vi.fn(),
  download: vi.fn(),
}));

vi.mock("hooks/useUsersQuery", () => ({
  useUsersQuery: () => ({
    data: state.failed ? undefined : { users: state.users, total: state.total ?? state.users.length },
    isLoading: false,
    isError: !!state.failed,
    error: state.failed,
    isPlaceholderData: state.placeholder,
    refetch: state.refetch,
  }),
  downloadUsersCsv: state.download,
}));

// The reseller filter needs the role and the admin list; neither is under test
// here, so they are stubbed instead of standing up a QueryClient.
vi.mock("hooks/useCurrentAdminQuery", () => ({ useIsSudo: () => state.sudo }));
vi.mock("hooks/useAdminsQuery", () => ({ useAdminsQuery: () => ({ data: state.admins }) }));
vi.mock("hooks/useSystemStatsQuery", () => ({
  useSystemStatsQuery: () => ({
    data: { total_user: 120, online_users: 4, users_active: 70, users_limited: 12, users_expired: 15, users_on_hold: 13, users_disabled: 10 },
  }),
}));

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

describe("Users list failures", () => {
  afterEach(() => {
    state.failed = null;
    state.refetch.mockReset();
    state.download.mockReset();
    state.users = [];
  });

  it("says the list could not be loaded, offers a retry, and does not claim no user matched", () => {
    state.failed = new Error("boom");
    state.users = [];
    render(<UsersTable />);
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
    expect(screen.queryByText(/no user matched/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(state.refetch).toHaveBeenCalledTimes(1);
  });

  it("falls back to its own words when the failure has none", () => {
    state.failed = {};
    render(<UsersTable />);
    expect(screen.getByRole("alert")).toHaveTextContent("Could not load the users.");
  });

  it("says an export failed, and clears that on the next try", async () => {
    state.download.mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce(undefined);
    render(<UsersTable />);
    fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not export the users.");
    fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });
});

describe("Users list paging", () => {
  afterEach(() => {
    state.total = null;
    state.placeholder = false;
    state.users = [];
    act(() => useUsersUiStore.getState().setFilters({ offset: 0, limit: 10 }));
  });

  const openAt = (offset: number, total: number) => {
    state.users = [];
    state.total = total;
    act(() => useUsersUiStore.getState().setFilters({ offset, limit: 10 }));
    render(<UsersTable />);
    return () => useUsersUiStore.getState().filters.offset;
  };

  it("steps back to the last real page after its only rows were deleted", () => {
    expect(openAt(20, 15)()).toBe(10);
  });

  it("lands on the last page exactly when the total is a whole number of pages", () => {
    expect(openAt(30, 30)()).toBe(20);
  });

  it("goes to the first page when nothing is left", () => {
    expect(openAt(10, 0)()).toBe(0);
  });

  it("stays put on a page that exists", () => {
    expect(openAt(10, 15)()).toBe(10);
  });

  it("does not act on the previous page's numbers while the next one loads", () => {
    state.placeholder = true;
    expect(openAt(20, 15)()).toBe(20);
  });
});

describe("Users table in Persian", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("writes the expiry and the last-seen time in Persian, not English inside a Persian sentence", async () => {
    await i18n.changeLanguage("fa");
    renderUser({ online: false, online_at: ago(7200), expire: Math.floor(NOW / 1000) + 3 * 86400 });
    expect(screen.getByText(/پایان در .*۳ روز/)).toBeInTheDocument();
    expect(screen.getByText(/۲ ساعت.* پیش آنلاین بوده/)).toBeInTheDocument();
  });
});

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
    expect(screen.getByText(/last seen .*10 min.* ago/)).toBeInTheDocument();
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

describe("Users list layout", () => {
  it("lays the cards out in a responsive grid that fills the width instead of one full-width card per row", () => {
    state.users = [user({ username: "a" }), user({ id: 2, username: "b" }), user({ id: 3, username: "c" })];
    const { container } = render(<UsersTable />);
    const grid = container.querySelector("div.grid.gap-4") as HTMLElement;
    expect(grid.className).toContain("auto-fill");
    expect(grid.className).toContain("min(21rem,100%)");
    expect(grid.children).toHaveLength(3);
  });

  it("shows the headline counters above the list: shown out of total, active, limited, expired, online", () => {
    state.users = [user({ username: "a" })];
    render(<UsersTable />);
    const strip = screen.getByLabelText("User statistics");
    expect(strip).toHaveTextContent("Shown1 / 1");
    expect(strip).toHaveTextContent("Active70");
    expect(strip).toHaveTextContent("Limited12");
    expect(strip).toHaveTextContent("Expired15");
    expect(strip).toHaveTextContent("Online Now4");
  });

  it("keeps the everyday actions on the card, the rare ones behind a menu, and Delete on its own", () => {
    state.users = [user({ username: "a" })];
    render(<UsersTable />);
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Subscription link" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reset Usage" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    expect(screen.getByRole("menuitem", { name: "Reset Usage" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Revoke Subscription" })).toBeInTheDocument();
  });

  it("opens the reset dialog from the menu and closes the menu", () => {
    state.users = [user({ username: "a" })];
    useUsersUiStore.setState({ resetUsageUser: null });
    render(<UsersTable />);
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Reset Usage" }));
    expect(useUsersUiStore.getState().resetUsageUser?.username).toBe("a");
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("draws the data limit as a ring with the used and total amounts, and an unlimited user without one", () => {
    state.users = [
      user({ username: "capped", used_traffic: 5 * 1024 ** 3, data_limit: 10 * 1024 ** 3 }),
      user({ id: 2, username: "free", used_traffic: 1024 ** 3, data_limit: null }),
    ];
    render(<UsersTable />);
    expect(screen.getByRole("img", { name: "Traffic used: 50 %" })).toBeInTheDocument();
    expect(screen.getAllByLabelText("Unlimited")).toHaveLength(1);
  });
});
