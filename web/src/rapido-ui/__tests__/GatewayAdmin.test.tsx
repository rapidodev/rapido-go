import { fireEvent, render, screen, within } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { GatewayPeer } from "types/Gateway";

const state = vi.hoisted(() => ({
  peers: [] as unknown[],
  update: vi.fn(),
  remove: vi.fn(),
}));

vi.mock("hooks/useGatewayQuery", () => ({
  useGatewaySettingsQuery: () => ({ data: { name: "here", secret: "test-only-secret" }, isLoading: false, isError: false, refetch: vi.fn() }),
  useGatewayPeersQuery: () => ({ data: state.peers, isLoading: false, isError: false }),
  useUpdateGatewaySettingsMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useCreateGatewayPeerMutation: () => ({ mutate: vi.fn(), isPending: false }),
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
});

beforeEach(() => {
  state.update.mockReset();
  state.remove.mockReset();
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
    const warning = within(card()).getByText(/Remove this peer\?/);
    expect(warning).toHaveTextContent("disappear from every user's subscription");
    expect(warning).toHaveTextContent("Open connections are not cut");
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
