import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { TunnelRelay } from "types/TunnelRelay";

const state: { relays: TunnelRelay[] } = { relays: [] };
const createMutate = vi.fn();
const deleteMutate = vi.fn();

vi.mock("hooks/useTunnelRelaysQuery", () => ({
  useTunnelRelaysQuery: () => ({ data: state.relays, isLoading: false, isError: false }),
  useCreateTunnelRelayMutation: () => ({ mutate: createMutate, isPending: false }),
  useDeleteTunnelRelayMutation: () => ({ mutate: deleteMutate, isPending: false }),
}));

import { TunnelRelaysAdmin } from "../TunnelRelaysAdmin";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

beforeEach(() => {
  createMutate.mockReset();
  deleteMutate.mockReset();
  state.relays = [];
});

const relay = (extra: Partial<TunnelRelay> = {}): TunnelRelay => ({
  id: 1,
  name: "node1-relay",
  host: "5.202.4.97",
  port: 20004,
  up: true,
  error: null,
  checked_at: new Date().toISOString(),
  ...extra,
});

const click = (el: HTMLElement) => fireEvent.click(el);
const enter = (el: HTMLElement, value: string) => fireEvent.change(el, { target: { value } });

describe("TunnelRelaysAdmin list", () => {
  it("shows the empty state when there are no relays", () => {
    render(<TunnelRelaysAdmin />);
    expect(screen.getByText(/No tunnel relays yet/)).toBeInTheDocument();
  });

  it("counts only relays whose up is exactly true toward the summary", () => {
    state.relays = [
      relay({ id: 1, up: true }),
      relay({ id: 2, up: false }),
      relay({ id: 3, up: null }),
    ];
    render(<TunnelRelaysAdmin />);
    expect(screen.getByText("1 of 3 relays up")).toBeInTheDocument();
  });

  it("shows an Up badge for an up relay, Down for a down one, and 'Not probed yet' for null", () => {
    state.relays = [
      relay({ id: 1, name: "up-relay", up: true }),
      relay({ id: 2, name: "down-relay", up: false, error: "dial tcp: timeout" }),
      relay({ id: 3, name: "fresh-relay", up: null, error: null, checked_at: null }),
    ];
    render(<TunnelRelaysAdmin />);
    expect(screen.getByText("Up")).toBeInTheDocument();
    expect(screen.getByText("Down")).toBeInTheDocument();
    expect(screen.getByText("Not probed yet")).toBeInTheDocument();
    // The error is shown on the down card, not on the up/unknown ones.
    expect(screen.getByText("dial tcp: timeout")).toBeInTheDocument();
  });

  it("shows host and port for each relay", () => {
    state.relays = [relay({ host: "185.124.175.44", port: 20001 })];
    render(<TunnelRelaysAdmin />);
    expect(screen.getByText("185.124.175.44")).toBeInTheDocument();
    expect(screen.getByText("20001")).toBeInTheDocument();
  });

  it("does not show a 'last checked' line for a relay that has never been probed", () => {
    state.relays = [relay({ up: null, error: null, checked_at: null })];
    render(<TunnelRelaysAdmin />);
    expect(screen.queryByText(/Last checked/)).not.toBeInTheDocument();
  });
});

describe("TunnelRelaysAdmin create flow", () => {
  it("disables submit until name, host and a valid port are all filled", () => {
    render(<TunnelRelaysAdmin />);
    click(screen.getByRole("button", { name: /Add tunnel relay/ }));

    const submit = screen.getByRole("button", { name: "Add tunnel relay" });
    expect(submit).toBeDisabled();

    enter(screen.getByLabelText("Name"), "node2-relay");
    expect(submit).toBeDisabled();
    enter(screen.getByLabelText("Host"), "185.124.175.44");
    expect(submit).toBeDisabled();
    enter(screen.getByLabelText(/^Port/), "20001");
    expect(submit).not.toBeDisabled();
  });

  it("rejects an out-of-range port", () => {
    render(<TunnelRelaysAdmin />);
    click(screen.getByRole("button", { name: /Add tunnel relay/ }));
    enter(screen.getByLabelText("Name"), "x");
    enter(screen.getByLabelText("Host"), "1.2.3.4");
    enter(screen.getByLabelText(/^Port/), "70000");
    expect(screen.getByRole("button", { name: "Add tunnel relay" })).toBeDisabled();
  });

  it("submits exactly the three fields, with port as a number", () => {
    render(<TunnelRelaysAdmin />);
    click(screen.getByRole("button", { name: /Add tunnel relay/ }));
    enter(screen.getByLabelText("Name"), "node2-relay");
    enter(screen.getByLabelText("Host"), "185.124.175.44");
    enter(screen.getByLabelText(/^Port/), "20001");
    click(screen.getByRole("button", { name: "Add tunnel relay" }));

    expect(createMutate).toHaveBeenCalledWith(
      { name: "node2-relay", host: "185.124.175.44", port: 20001 },
      expect.anything()
    );
  });

  it("shows the server's error message and keeps the form open on failure", async () => {
    createMutate.mockImplementation((_body, opts) =>
      opts.onError({ response: { _data: { detail: "boom" } } })
    );
    render(<TunnelRelaysAdmin />);
    click(screen.getByRole("button", { name: /Add tunnel relay/ }));
    enter(screen.getByLabelText("Name"), "x");
    enter(screen.getByLabelText("Host"), "1.2.3.4");
    enter(screen.getByLabelText(/^Port/), "1");
    click(screen.getByRole("button", { name: "Add tunnel relay" }));

    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
  });
});

describe("TunnelRelaysAdmin delete flow", () => {
  it("requires a confirm click before actually deleting", () => {
    state.relays = [relay()];
    render(<TunnelRelaysAdmin />);
    click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteMutate).not.toHaveBeenCalled();

    expect(screen.getByText("Delete this tunnel relay?")).toBeInTheDocument();
    click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteMutate).toHaveBeenCalledWith(1, expect.anything());
  });

  it("cancel backs out of the confirm step without deleting", () => {
    state.relays = [relay()];
    render(<TunnelRelaysAdmin />);
    click(screen.getByRole("button", { name: "Delete" }));
    click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("Delete this tunnel relay?")).not.toBeInTheDocument();
    expect(deleteMutate).not.toHaveBeenCalled();
  });
});
