import { fireEvent, render, screen, within } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  overviewAsked: [] as boolean[],
  overview: undefined as unknown,
  disable: vi.fn(),
  activate: vi.fn(),
  reset: vi.fn(),
  remove: vi.fn(),
}));

vi.mock("hooks/useAdminsQuery", () => ({
  useAdminsQuery: () => ({
    data: [
      { username: "reseller-a", is_sudo: false, is_owner: false, telegram_id: null, discord_webhook: null, users_usage: 1024 ** 3 },
      { username: "me", is_sudo: true, is_owner: true, telegram_id: null, discord_webhook: null, users_usage: 0 },
    ],
    isLoading: false,
    isError: false,
  }),
  useAdminsOverviewQuery: (enabled: boolean) => {
    state.overviewAsked.push(enabled);
    return { data: enabled ? state.overview : undefined };
  },
  useCreateAdminMutation: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateAdminMutation: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteAdminMutation: () => ({ mutate: state.remove, isPending: false }),
  useDisableAdminUsersMutation: () => ({ mutate: state.disable, isPending: false }),
  useActivateAdminUsersMutation: () => ({ mutate: state.activate, isPending: false }),
  useResetAdminUsageMutation: () => ({ mutate: state.reset, isPending: false }),
}));
vi.mock("hooks/useCurrentAdminQuery", () => ({
  useCurrentAdminQuery: () => ({ data: { username: "me", is_sudo: true, is_owner: true } }),
}));

import { AdminsAdmin } from "../AdminsAdmin";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  for (const mock of [state.disable, state.activate, state.reset, state.remove]) mock.mockReset();
  state.overviewAsked = [];
  state.overview = {
    admins: [{ username: "reseller-a", online: 3, configs: { total: 12, active: 7, disabled: 2, limited: 1, expired: 1, on_hold: 1 } }],
  };
});

// Numbers interpolated into a sentence carry bidi isolates; what is read has none.
const plain = (el: HTMLElement) => (el.textContent ?? "").replace(/[⁦⁩]/g, "");
const card = () => within(screen.getByText("reseller-a").closest(".rp-card") as HTMLElement);
const click = (name: string) => fireEvent.click(card().getByRole("button", { name }));

describe("bulk actions on an admin", () => {
  it("asks before disabling their users, with how many that hits", () => {
    render(<AdminsAdmin />);
    click("Disable their users");
    expect(state.disable).not.toHaveBeenCalled();
    const warning = card().getByText(/is disabled and disconnected within moments/);
    expect(plain(warning)).toContain("Users affected right now: 8.");
    click("Disable their users");
    expect(state.disable.mock.calls[0][0]).toBe("reseller-a");
  });

  it("asks before activating their users, says who comes back, and that it cannot be undone as a group", () => {
    render(<AdminsAdmin />);
    click("Activate their users");
    expect(state.activate).not.toHaveBeenCalled();
    const warning = card().getByText(/becomes active and can connect again/);
    expect(warning).toHaveTextContent("including anyone who was disabled on purpose");
    expect(warning).toHaveTextContent("cannot be undone");
    expect(plain(warning)).toContain("Users affected right now: 2.");
    click("Activate their users");
    expect(state.activate.mock.calls[0][0]).toBe("reseller-a");
  });

  it("asks before resetting the usage counter, naming the amount and that nobody is disconnected", () => {
    render(<AdminsAdmin />);
    click("Reset usage counter");
    expect(state.reset).not.toHaveBeenCalled();
    const warning = card().getByText(/goes back to zero/);
    expect(plain(warning)).toContain("1 GB");
    expect(warning).toHaveTextContent("Nobody is disconnected");
    expect(warning).toHaveTextContent("cannot be undone");
    click("Reset usage counter");
    expect(state.reset.mock.calls[0][0]).toBe("reseller-a");
  });

  it("says what deleting really does: the panel refuses while they own users", () => {
    render(<AdminsAdmin />);
    click("Delete");
    expect(state.remove).not.toHaveBeenCalled();
    const warning = card().getByText(/Permanently deletes this admin's account/);
    expect(warning).toHaveTextContent("The panel refuses while they still own users");
    expect(warning).toHaveTextContent("cannot be undone");
    expect(plain(warning)).toContain("Users they still own: 12.");
    click("Delete");
    expect(state.remove.mock.calls[0][0]).toBe("reseller-a");
  });

  it("gives the warnings without a number when the panel has not said one", () => {
    state.overview = undefined;
    render(<AdminsAdmin />);
    click("Disable their users");
    expect(card().getByText(/is disabled and disconnected within moments/)).not.toHaveTextContent("Users affected");
  });

  it("only asks for the counts while a confirmation that names one is open", () => {
    render(<AdminsAdmin />);
    expect(state.overviewAsked.every((enabled) => !enabled)).toBe(true);
    click("Reset usage counter");
    expect(state.overviewAsked.every((enabled) => !enabled)).toBe(true);
    click("Disable their users");
    expect(state.overviewAsked).toContain(true);
  });

  it("shows one confirmation at a time, and Cancel closes it without doing anything", () => {
    render(<AdminsAdmin />);
    click("Disable their users");
    click("Activate their users");
    expect(card().queryByText(/is disabled and disconnected within moments/)).not.toBeInTheDocument();
    expect(card().getByText(/becomes active and can connect again/)).toBeInTheDocument();
    click("Cancel");
    expect(card().queryByText(/becomes active and can connect again/)).not.toBeInTheDocument();
    expect(state.activate).not.toHaveBeenCalled();
  });

  it("offers no Delete on your own card", () => {
    render(<AdminsAdmin />);
    const mine = within(screen.getByText("me").closest(".rp-card") as HTMLElement);
    expect(mine.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });
});

describe("the admin form", () => {
  it("keeps a Telegram ID typed on a Persian keyboard", () => {
    render(<AdminsAdmin />);
    fireEvent.click(screen.getByRole("button", { name: /New admin/ }));
    const field = screen.getByLabelText(/Telegram ID/);
    fireEvent.change(field, { target: { value: "۱۲۳۴۵" } });
    expect(field).toHaveValue("12345");
    fireEvent.change(field, { target: { value: "-٦٧a" } });
    expect(field).toHaveValue("-67");
  });
});
