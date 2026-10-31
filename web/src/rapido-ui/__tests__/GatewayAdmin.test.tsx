import { fireEvent, render, screen, within } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { GatewayPeer } from "types/Gateway";

const state = vi.hoisted(() => ({
  peers: [] as unknown[],
  update: vi.fn(),
  remove: vi.fn(),
  create: vi.fn(),
  retry: vi.fn(),
  rejected: [] as unknown[],
}));

vi.mock("hooks/useGatewayQuery", () => ({
  useGatewaySettingsQuery: () => ({ data: { name: "here", secret: "test-only-secret" }, isLoading: false, isError: false, refetch: vi.fn() }),
  useGatewayPeersQuery: () => ({ data: state.peers, isLoading: false, isError: false }),
  useUpdateGatewaySettingsMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useCreateGatewayPeerMutation: () => ({ mutate: state.create, isPending: false }),
  useGatewayRejectedQuery: () => ({ data: state.rejected, isLoading: false, isError: false }),
  useRetryGatewayRejectedMutation: () => ({ mutate: state.retry, isPending: false }),
  useUpdateGatewayPeerMutation: () => ({ mutate: state.update, isPending: false }),
  useDeleteGatewayPeerMutation: () => ({ mutate: state.remove, isPending: false }),
  useTestGatewayPeerMutation: () => ({ mutate: vi.fn(), isPending: false }),
}));

import { GatewayAdmin } from "../GatewayAdmin";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

const peer = (id: number, name: string): GatewayPeer => ({
  id,
  name,
  base_url: `https://${name}.example.com`,
  secret: `test-only-secret-of-${name}`,
  enabled: true,
  last_seen_at: "2026-10-31T09:00:00Z",
  last_error: "",
  crowdedness: 12,
  hosts: [{ remark: `${name} germany`, protocol: "vless", network: "tcp", security: "tls", address: `${name}.example.com`, port: 443 }],
  synced_users: 30,
  rejected_users: 0,
  total_users: 40,
});

beforeEach(() => {
  state.update.mockReset();
  state.remove.mockReset();
  state.create.mockReset();
  state.retry.mockReset();
  state.rejected = [];
  state.peers = [peer(1, "alpha"), peer(2, "beta")];
});

const editButtons = () => screen.getAllByRole("button", { name: "Edit" });

describe("editing peers", () => {
  it("opens another peer's data when moving from one Edit to the next, and saves it onto that peer", () => {
    render(<GatewayAdmin />);
    fireEvent.click(editButtons()[0]);
    expect(screen.getByDisplayValue("alpha")).toBeInTheDocument();

    fireEvent.click(editButtons()[1]);
    expect(screen.queryByDisplayValue("alpha")).not.toBeInTheDocument();
    expect(screen.getByDisplayValue("beta")).toBeInTheDocument();
    expect(screen.getByDisplayValue("https://beta.example.com")).toBeInTheDocument();
    expect(screen.getByDisplayValue("test-only-secret-of-beta")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(state.update).toHaveBeenCalledTimes(1);
    expect(state.update.mock.calls[0][0]).toEqual({
      id: 2,
      body: { name: "beta", base_url: "https://beta.example.com", secret: "test-only-secret-of-beta", enabled: true },
    });
  });

  it("starts empty when moving from an Edit to Add peer", () => {
    render(<GatewayAdmin />);
    fireEvent.click(editButtons()[0]);
    fireEvent.click(screen.getByRole("button", { name: /Add peer/ }));
    expect(screen.queryByDisplayValue("alpha")).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText("EU Panel")).toHaveValue("");
  });
});

describe("removing a peer", () => {
  const card = () => screen.getByText("alpha").closest(".rp-card") as HTMLElement;

  it("says what happens to subscriptions, sync and connections before doing it", () => {
    render(<GatewayAdmin />);
    fireEvent.click(within(card()).getByRole("button", { name: "Delete" }));
    expect(state.remove).not.toHaveBeenCalled();
    const warning = within(card()).getByText(/Remove this panel\?/);
    expect(warning).toHaveTextContent("disappear from every user's subscription");
    expect(warning).toHaveTextContent("anyone connected through it is cut off");
    expect(warning).toHaveTextContent("can't be undone");
    fireEvent.click(within(card()).getByRole("button", { name: "Delete" }));
    expect(state.remove.mock.calls[0][0]).toBe(1);
  });

  it("shows why it failed instead of doing nothing", () => {
    state.remove.mockImplementation((_id, opts) => opts.onError({ response: { _data: { detail: "Could not reach the database" } } }));
    render(<GatewayAdmin />);
    fireEvent.click(within(card()).getByRole("button", { name: "Delete" }));
    fireEvent.click(within(card()).getByRole("button", { name: "Delete" }));
    expect(within(card()).getByText("Could not reach the database")).toBeInTheDocument();
    // The confirmation closed, so the failed click can simply be tried again.
    expect(within(card()).queryByText(/Remove this peer\?/)).not.toBeInTheDocument();
  });

  it("has words of its own when the failure has none", () => {
    state.remove.mockImplementation((_id, opts) => opts.onError({}));
    render(<GatewayAdmin />);
    fireEvent.click(within(card()).getByRole("button", { name: "Delete" }));
    fireEvent.click(within(card()).getByRole("button", { name: "Delete" }));
    expect(within(card()).getByText("Could not remove this peer.")).toBeInTheDocument();
  });
});

describe("a peer's state", () => {
  it("shows how far the copy has got, what the peer offers and what went wrong", () => {
    state.peers = [{ ...peer(1, "alpha"), last_error: "could not reach peer: timeout" }];
    render(<GatewayAdmin />);
    expect(screen.getByText(/30 of 40 users copied/)).toBeInTheDocument();
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "75");
    expect(screen.getByRole("alert")).toHaveTextContent("could not reach peer: timeout");

    expect(screen.queryByText("alpha germany")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Servers (1)" }));
    expect(screen.getByText("alpha germany")).toBeInTheDocument();
    expect(screen.getByText(/alpha\.example\.com:443/)).toBeInTheDocument();
  });

  it("says who was refused and why, and can ask again", () => {
    state.peers = [{ ...peer(1, "alpha"), rejected_users: 1 }];
    state.rejected = [{ username: "taken_name", detail: "a real local user exists there", at: "2026-10-31T09:00:00Z" }];
    render(<GatewayAdmin />);
    expect(screen.getByText("1 refused")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Who was refused" }));
    expect(screen.getByText("taken_name")).toBeInTheDocument();
    expect(screen.getByText("a real local user exists there")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Ask again now" }));
    expect(state.retry).toHaveBeenCalledWith(1);
  });

  it("copes with a peer that has never been reached", () => {
    state.peers = [{ ...peer(1, "alpha"), enabled: false, last_seen_at: null, crowdedness: null, hosts: null, synced_users: 0 }];
    render(<GatewayAdmin />);
    expect(screen.getByText(/Not reached yet/)).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Servers (0)" }));
    expect(screen.getByText(/Not known yet/)).toBeInTheDocument();
  });
});

describe("switching a peer on and off", () => {
  it("adds a new peer switched off", () => {
    state.peers = [];
    render(<GatewayAdmin />);
    fireEvent.click(screen.getByRole("button", { name: /Add/ }));
    const inputs = screen.getAllByRole("textbox").filter((el) => el.tagName === "INPUT");
    fireEvent.change(inputs[inputs.length - 3], { target: { value: "gamma" } });
    fireEvent.change(inputs[inputs.length - 2], { target: { value: "https://gamma.example.com" } });
    fireEvent.change(inputs[inputs.length - 1], { target: { value: "test-only-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(state.create.mock.calls[0][0]).toMatchObject({ name: "gamma", enabled: false });
  });

  it("explains what starting does before it does it", () => {
    state.peers = [{ ...peer(1, "alpha"), enabled: false, synced_users: 0 }];
    render(<GatewayAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(state.update).not.toHaveBeenCalled();
    expect(screen.getByText(/All 40 users, with their credentials/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Yes, copy the users and start" }));
    expect(state.update.mock.calls[0][0]).toEqual({
      id: 1,
      body: { name: "alpha", base_url: "https://alpha.example.com", secret: "test-only-secret-of-alpha", enabled: true },
    });
  });

  it("explains what stopping does, and can be called off", () => {
    state.peers = [peer(1, "alpha")];
    render(<GatewayAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(screen.getByText(/leave every user's subscription/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(state.update).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, stop offering it" }));
    expect(state.update.mock.calls[0][0].body.enabled).toBe(false);
  });

  it("warns when removal left working copies on a peer it couldn't reach", () => {
    state.peers = [peer(1, "alpha")];
    state.remove.mockImplementation((_id: number, options: { onSuccess?: (r: unknown) => void }) =>
      options.onSuccess?.({ deleted: true, copies_removed: 0, copies_left: true })
    );
    render(<GatewayAdmin />);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/alpha was removed, but it couldn't be reached/);
  });
});

