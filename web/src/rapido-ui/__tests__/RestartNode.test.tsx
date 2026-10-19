import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { formatTook, rebootWaitState, restartConfirmed } from "utils/nodeRestart";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { RestartNodeButton } from "../RestartNodeModal";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

let nodeStatus = "connected";
beforeEach(() => {
  fetchMock.mockReset();
  nodeStatus = "connected";
  fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
    if (url === "/nodes") return Promise.resolve([{ id: 5, name: "edge-1", status: nodeStatus }]);
    if (url.endsWith("/restart")) return Promise.resolve({ ok: true, mode: "service", detail: "restarted rapido-node", active: true, took_ms: 2300 });
    return Promise.resolve({});
  });
});

const renderButton = (ssh_known?: boolean) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <RestartNodeButton node={{ id: 5, name: "edge-1", ssh_known }} />
    </QueryClientProvider>
  );
};
const open = () => fireEvent.click(screen.getByRole("button", { name: "Restart node" }));
const calls = () => fetchMock.mock.calls.filter((c) => String(c[0]).endsWith("/restart"));

describe("restart node dialog", () => {
  it("opens from the button with the two choices, the low-risk one selected", () => {
    renderButton(true);
    open();
    const group = screen.getByRole("radiogroup", { name: "What do you want to restart?" });
    const [service, reboot] = within(group).getAllByRole("radio");
    expect(service).toHaveAttribute("aria-checked", "true");
    expect(reboot).toHaveAttribute("aria-checked", "false");
    expect(service).toHaveTextContent("Restart agent");
    expect(service).toHaveTextContent("Short, low risk");
    expect(reboot).toHaveTextContent("Reboot server");
    expect(reboot).toHaveTextContent("Long, high risk");
    // each explains its impact in plain words
    expect(service).toHaveTextContent(/tunnels are not touched/);
    expect(reboot).toHaveTextContent(/every user on this node is disconnected/);
  });

  it("renders in the page body, not inside the (tilting) card that holds the button", () => {
    const { container } = renderButton(true);
    open();
    expect(container.querySelector("[role=radiogroup]")).toBeNull();
    expect(document.body.querySelector("[role=radiogroup]")).not.toBeNull();
  });

  it("asks for SSH details only when the panel has no stored login, and says they are used once", () => {
    const first = renderButton(false);
    open();
    expect(screen.getByTestId("restart-ssh")).toBeInTheDocument();
    expect(screen.getByText(/used once to run the command and are not stored/)).toBeInTheDocument();
    first.unmount();
    renderButton(true);
    open();
    expect(screen.queryByTestId("restart-ssh")).toBeNull();
  });

  it("does not ask for SSH details from a panel that does not report ssh_known", () => {
    renderButton(undefined);
    open();
    expect(screen.queryByTestId("restart-ssh")).toBeNull();
  });

  it("needs the SSH password before it can run when the login is not stored", () => {
    renderButton(false);
    open();
    const confirm = screen.getByRole("button", { name: "Restart agent", description: "" });
    expect(confirm).toBeDisabled();
    fireEvent.change(document.querySelector('[data-testid="restart-ssh"] input[type="password"]') as HTMLInputElement, { target: { value: "pw" } });
    expect(confirm).toBeEnabled();
  });

  it("gates the reboot on typing the node's exact name", () => {
    renderButton(true);
    open();
    fireEvent.click(screen.getAllByRole("radio")[1]);
    const confirm = screen.getByRole("button", { name: "Reboot server", description: "" });
    expect(confirm).toBeDisabled();
    const input = screen.getByPlaceholderText("Node name");
    fireEvent.change(input, { target: { value: "edge-" } });
    expect(confirm).toBeDisabled();
    fireEvent.change(input, { target: { value: "EDGE-1" } });
    expect(confirm).toBeDisabled();
    fireEvent.change(input, { target: { value: "edge-1" } });
    expect(confirm).toBeEnabled();
  });

  it("restarts the agent with just the mode, then shows the result and how long it took", async () => {
    renderButton(true);
    open();
    fireEvent.click(screen.getByRole("button", { name: "Restart agent", description: "" }));
    await screen.findByText("Agent restarted in 2.3 s.");
    expect(screen.getByText("The node reports active.")).toBeInTheDocument();
    expect(calls()).toHaveLength(1);
    expect(calls()[0][0]).toBe("/node/5/restart");
    expect((calls()[0][1] as { body: unknown }).body).toEqual({ mode: "service" });
  });

  it("sends the confirmation name and the one-off SSH login with a reboot", async () => {
    renderButton(false);
    open();
    fireEvent.click(screen.getAllByRole("radio")[1]);
    fireEvent.change(screen.getByPlaceholderText("Node name"), { target: { value: "edge-1" } });
    const ssh = within(screen.getByTestId("restart-ssh"));
    fireEvent.change(ssh.getAllByRole("textbox")[0], { target: { value: "admin" } });
    fireEvent.change(document.querySelector('[data-testid="restart-ssh"] input[type="password"]') as HTMLInputElement, { target: { value: "pw" } });
    fireEvent.click(screen.getByRole("button", { name: "Reboot server", description: "" }));
    await waitFor(() => expect(calls()).toHaveLength(1));
    expect((calls()[0][1] as { body: unknown }).body).toEqual({
      mode: "reboot",
      confirm_name: "edge-1",
      ssh_user: "admin",
      ssh_port: 22,
      ssh_password: "pw",
    });
  });

  it("after a reboot, says it is waiting for the node to come back", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === "/nodes") return Promise.resolve([{ id: 5, name: "edge-1", status: "connecting" }]);
      return Promise.resolve({ ok: true, mode: "reboot", detail: "reboot scheduled" });
    });
    renderButton(true);
    open();
    fireEvent.click(screen.getAllByRole("radio")[1]);
    fireEvent.change(screen.getByPlaceholderText("Node name"), { target: { value: "edge-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Reboot server", description: "" }));
    const wait = await screen.findByTestId("reboot-wait");
    expect(wait).toHaveAttribute("data-wait", "waiting");
    expect(wait).toHaveTextContent(/Waiting for the node to come back/);
  });

  it("shows the server's error", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url === "/nodes") return Promise.resolve([]);
      return Promise.reject({ response: { _data: { detail: "ssh: connection refused" } } });
    });
    renderButton(true);
    open();
    fireEvent.click(screen.getByRole("button", { name: "Restart agent", description: "" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("ssh: connection refused");
  });
});

describe("restart helpers", () => {
  it("confirms a reboot only on the exact name; the agent restart needs nothing", () => {
    expect(restartConfirmed("service", "edge-1", "")).toBe(true);
    expect(restartConfirmed("reboot", "edge-1", "")).toBe(false);
    expect(restartConfirmed("reboot", "edge-1", "Edge-1")).toBe(false);
    expect(restartConfirmed("reboot", "edge-1", "edge-1")).toBe(true);
  });

  it("calls a reboot back only once the node is connected after going down (or a safe time has passed)", () => {
    expect(rebootWaitState({ elapsedMs: 4000, status: "connected", sawDown: false })).toBe("waiting");
    expect(rebootWaitState({ elapsedMs: 4000, status: "connecting", sawDown: true })).toBe("waiting");
    expect(rebootWaitState({ elapsedMs: 40_000, status: "connected", sawDown: true })).toBe("back");
    expect(rebootWaitState({ elapsedMs: 20_000, status: "connected", sawDown: false })).toBe("back");
    expect(rebootWaitState({ elapsedMs: 5 * 60_000, status: "error", sawDown: true })).toBe("timeout");
    expect(rebootWaitState({ elapsedMs: 5 * 60_000, status: "connected", sawDown: true })).toBe("back");
  });

  it("formats how long it took", () => {
    expect(formatTook(2300)).toBe("2.3 s");
    expect(formatTook(45_000)).toBe("45 s");
    expect(formatTook(undefined)).toBe("");
  });
});
