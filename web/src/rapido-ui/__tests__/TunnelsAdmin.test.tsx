import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import i18n from "locales/i18n";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { Tunnel, TunnelDiscoverResult } from "types/Tunnel";
import { useAppearance } from "../appearance";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("service/http", () => ({ fetch: fetchMock, fetcher: fetchMock }));

import { TunnelsAdmin } from "../TunnelsAdmin";
import { buildUpdatePayload } from "../TunnelEditModal";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});

const metrics = (over: Partial<NonNullable<Tunnel["metrics"]>> = {}): NonNullable<Tunnel["metrics"]> => ({
  cpu_percent: 37,
  mem_used_mb: 512,
  mem_total_mb: 2048,
  disk_used_gb: 10,
  disk_total_gb: 40,
  rx_bytes: 1024,
  tx_bytes: 2048,
  rx_dropped: 1,
  tx_dropped: 0,
  rx_errors: 0,
  tx_errors: 2,
  connections: 120,
  checked_at: new Date().toISOString(),
  nic: "eth0",
  nic_speed_mbps: 1000,
  nic_rx_bps: 250_000_000,
  nic_tx_bps: 50_000_000,
  nic_rx_bytes: 5_000_000,
  nic_tx_bytes: 6_000_000,
  ...over,
});

const tunnel = (over: Partial<Tunnel> = {}): Tunnel => ({
  id: 1,
  name: "de-tunnel",
  method: "frp_direct",
  node_id: 7,
  relay_host: "198.51.100.9",
  ports: [443, 8443],
  interface_name: "",
  frp_control_port: 7000,
  node_tunnel_addr: null,
  relay_tunnel_addr: null,
  status: "active",
  status_message: null,
  health: { up: true, checked_at: new Date().toISOString() },
  metrics: metrics(),
  managed: true,
  protocol: null,
  relay_addrs: [],
  node_ssh_port: 22,
  node_ssh_user: "root",
  relay_ssh_port: 2222,
  relay_ssh_user: "admin",
  node_nic: null,
  relay_nic: null,
  ...over,
});

const haproxyNode = {
  nic: "ens3",
  nic_speed_mbps: 10000,
  rx_bps: 1_000_000,
  tx_bps: 2_000_000,
  rx_bytes: 10,
  tx_bytes: 20,
  haproxy: {
    present: true,
    active: "relay6",
    servers: [
      { backend: "relay", name: "relay6", addr: "[2001:db8::1]:7000", status: "UP", backup: false, sessions: 42, total_sessions: 900, since_seconds: 3725 },
      { backend: "relay", name: "relay4", addr: "198.51.100.9:7000", status: "UP 1/2", backup: true, sessions: 0, total_sessions: 5, since_seconds: 30 },
      { backend: "relay", name: "relay5", addr: "198.51.100.10:7000", status: "DOWN", backup: true, sessions: 0, total_sessions: 0, since_seconds: 9 },
      { backend: "relay", name: "relay7", addr: "198.51.100.11:7000", status: "MAINT", backup: true, sessions: 0, total_sessions: 0, since_seconds: 1 },
    ],
  },
};

let tunnels: Tunnel[] = [];

beforeEach(() => {
  fetchMock.mockReset();
  tunnels = [];
  fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
    if (url === "/tunnels" && !opts?.method) return Promise.resolve(tunnels);
    if (url === "/nodes") return Promise.resolve([{ id: 7, name: "de-node", address: "1.1.1.1", port: 1, api_port: 2, status: "connected", usage_coefficient: 1 }]);
    return Promise.resolve({});
  });
  useAppearance.getState().reset();
});
afterEach(() => {
  vi.clearAllMocks();
});

const renderPage = () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <TunnelsAdmin />
    </QueryClientProvider>
  );
};

const calls = (method: string) => fetchMock.mock.calls.filter((c) => (c[1] as { method?: string } | undefined)?.method === method);

describe("tunnel card", () => {
  // The instruments (dials and dual rings) carry data-state; trend strips are images too but have none.
  const instruments = () => [...document.querySelectorAll("[role=img][data-state]")];

  it("draws one cluster: a dual ring for the node card and one for the relay card, then CPU, RAM, disk and connections rings, with the packet loss as a pill", async () => {
    tunnels = [tunnel({ metrics: metrics({ node: haproxyNode }), node_nic: "ens3" })];
    renderPage();
    await screen.findByText("de-tunnel");
    const labels = instruments().map((n) => n.getAttribute("aria-label"));
    // node NIC + relay NIC dual rings, then cpu, ram, disk, connections
    expect(labels).toHaveLength(6);
    expect(labels).toContain("CPU: 37 %");
    expect(labels).toContain("RAM: 25 %");
    expect(labels).toContain("Disk: 25 %");
    expect(labels).toContain("Relay: ↓ 250 Mbps, ↑ 50 Mbps");
    expect(labels).toContain("Node de-node: ↓ 1 Mbps, ↑ 2 Mbps");
    // drops + errors: 1 + 0 + 0 + 2
    expect(screen.getByTestId("loss-pill")).toHaveTextContent("Packet loss 3");
    expect(screen.getByText("eth0 · 1 Gbps")).toBeInTheDocument();
    expect(screen.getByText("ens3 · 10 Gbps")).toBeInTheDocument();
  });

  it("scales a bandwidth dial to the card's link speed when known, and says it is auto-scaled when not", async () => {
    tunnels = [tunnel({ metrics: metrics({ nic_speed_mbps: undefined }) })];
    renderPage();
    await screen.findByText("de-tunnel");
    expect(screen.getAllByText(/auto scale/).length).toBeGreaterThan(0);
    expect(screen.queryByText("of 1 Gbps link")).toBeNull();
  });

  it("treats missing bandwidth fields as unknown, not zero", async () => {
    tunnels = [tunnel({ metrics: metrics({ nic_rx_bps: undefined, nic_tx_bps: undefined, nic_speed_mbps: undefined }) })];
    renderPage();
    await screen.findByText("de-tunnel");
    expect(instruments().map((n) => n.getAttribute("aria-label"))).toContain("Relay: ↓ unknown, ↑ unknown");
  });

  it("falls back to classic bars when the user chose them", async () => {
    useAppearance.getState().set({ meters: "bars" });
    tunnels = [tunnel()];
    renderPage();
    await screen.findByText("de-tunnel");
    // the resource rings become segmented bars; the dual rings stay rings
    expect(instruments()).toHaveLength(1);
    expect(screen.getByRole("meter", { name: "CPU" })).toHaveAttribute("aria-valuenow", "37");
  });

  it("shows the protocol as a chip and a Detected badge for a detected tunnel", async () => {
    tunnels = [tunnel({ managed: false, method: "external", protocol: "frp over TCP via HAProxy (IPv6 with IPv4 backup)" })];
    renderPage();
    await screen.findByText("de-tunnel");
    expect(screen.getByTestId("protocol-chip")).toHaveTextContent("frp over TCP via HAProxy (IPv6 with IPv4 backup)");
    expect(screen.getByText("Detected")).toBeInTheDocument();
  });

  it("hides Start, Stop and Restart for a detected tunnel but not for a managed one", async () => {
    tunnels = [tunnel({ id: 1, name: "built" }), tunnel({ id: 2, name: "found", managed: false, method: "external" })];
    renderPage();
    await screen.findByText("found");
    const [built, found] = screen.getAllByTestId("tunnel-card");
    expect(within(built).getByRole("button", { name: "Restart" })).toBeInTheDocument();
    expect(within(built).getByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(within(found).queryByRole("button", { name: "Restart" })).toBeNull();
    expect(within(found).queryByRole("button", { name: "Stop" })).toBeNull();
    expect(within(found).queryByRole("button", { name: "Start" })).toBeNull();
    // both can be edited
    expect(within(built).getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(within(found).getByRole("button", { name: "Edit" })).toBeInTheDocument();
  });

  it("says deleting a detected tunnel only removes it from the panel", async () => {
    tunnels = [tunnel({ managed: false, method: "external" })];
    renderPage();
    await screen.findByText("de-tunnel");
    fireEvent.click(screen.getByRole("button", { name: "Remove from panel" }));
    expect(screen.getByText(/Nothing on the node or relay is touched/)).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Remove from panel" })[0]);
    await waitFor(() => expect(calls("DELETE")).toHaveLength(1));
    expect(calls("DELETE")[0][0]).toBe("/tunnels/1");
  });

  it("keeps the teardown wording for a managed tunnel", async () => {
    tunnels = [tunnel()];
    renderPage();
    await screen.findByText("de-tunnel");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(screen.getByText(/Both ends \(node and relay\) will be torn down/)).toBeInTheDocument();
  });
});

describe("HAProxy failover panel", () => {
  it("lists every server with a coloured status pill, the backup badge, address, sessions and how long the status held", async () => {
    tunnels = [tunnel({ metrics: metrics({ node: haproxyNode }) })];
    renderPage();
    await screen.findByText("de-tunnel");
    const panel = screen.getByRole("region", { name: "HAProxy failover" });
    const pills = within(panel).getAllByTestId("haproxy-status");
    expect(pills.map((p) => p.textContent)).toEqual(["UP", "UP 1/2", "DOWN", "MAINT"]);
    expect(pills[0]).toHaveClass("text-emerald-300");
    expect(pills[1]).toHaveClass("text-yellow-300");
    expect(pills[2]).toHaveClass("text-red-300");
    expect(pills[3]).toHaveClass("text-rapido-muted");
    expect(within(panel).getAllByText("backup")).toHaveLength(3);
    expect(within(panel).getByText("[2001:db8::1]:7000")).toBeInTheDocument();
    expect(within(panel).getByText("UP for 1h2m5s")).toBeInTheDocument();
    expect(within(panel).getByText("DOWN for 9s")).toBeInTheDocument();
  });

  it("marks exactly the active path, and raises no failover alarm while the primary carries traffic", async () => {
    tunnels = [tunnel({ metrics: metrics({ node: haproxyNode }) })];
    renderPage();
    await screen.findByText("de-tunnel");
    const panel = screen.getByRole("region", { name: "HAProxy failover" });
    const rows = within(panel).getAllByRole("listitem");
    expect(rows.map((r) => r.getAttribute("data-active"))).toEqual(["true", "false", "false", "false"]);
    expect(within(rows[0]).getByTestId("haproxy-active")).toHaveTextContent("Carrying traffic");
    expect(within(panel).queryByRole("alert")).toBeNull();
    // the active route has flowing dots, the others do not
    expect(rows[0].querySelector(".rp-flow-dots")).not.toBeNull();
    expect(rows[0].querySelector(".animate-rp-flow")).not.toBeNull();
    expect(rows[1].querySelector(".animate-rp-flow")).toBeNull();
  });

  it("makes a failover obvious: the backup is lit and a banner says so", async () => {
    const failed = { ...haproxyNode, haproxy: { ...haproxyNode.haproxy, active: "relay/relay4" } };
    failed.haproxy.servers = failed.haproxy.servers.map((s) => (s.name === "relay6" ? { ...s, status: "DOWN" } : s));
    tunnels = [tunnel({ metrics: metrics({ node: failed }) })];
    renderPage();
    await screen.findByText("de-tunnel");
    const panel = screen.getByRole("region", { name: "HAProxy failover" });
    expect(within(panel).getByRole("alert")).toHaveTextContent("Failover in effect");
    const rows = within(panel).getAllByRole("listitem");
    expect(rows.map((r) => r.getAttribute("data-active"))).toEqual(["false", "true", "false", "false"]);
  });

  it("is absent when HAProxy is not present", async () => {
    tunnels = [tunnel({ metrics: metrics({ node: { ...haproxyNode, haproxy: { present: false } } }) })];
    renderPage();
    await screen.findByText("de-tunnel");
    expect(screen.queryByRole("region", { name: "HAProxy failover" })).toBeNull();
  });
});

describe("edit tunnel", () => {
  it("builds a PATCH with only the changed fields and never an empty password", () => {
    const base = { name: "a", node_ssh_port: 22, node_ssh_user: "root", relay_host: "h", relay_ssh_port: 22, relay_ssh_user: "root", ports: [443, 80] };
    const same = { name: "a", nodeSshPort: "22", nodeSshUser: "root", nodeSshPassword: "", relayHost: "h", relaySshPort: "22", relaySshUser: "root", relaySshPassword: "", ports: [80, 443] };
    expect(buildUpdatePayload(base, same)).toEqual({});
    expect(buildUpdatePayload(base, { ...same, name: "b", relaySshPassword: "s3cret", ports: [80, 443, 8443] })).toEqual({
      name: "b",
      relay_ssh_password: "s3cret",
      ports: [80, 443, 8443],
    });
    expect(buildUpdatePayload(base, { ...same, nodeSshPort: "2200", relayHost: "other" })).toEqual({
      node_ssh_port: 2200,
      relay_host: "other",
    });
  });

  it("opens for a managed tunnel, warns about the reconnect when ports change, and PATCHes", async () => {
    tunnels = [tunnel()];
    renderPage();
    await screen.findByText("de-tunnel");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByText("Edit de-tunnel");
    expect(dialog).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    // password fields start empty and say empty keeps the current one
    screen.getAllByPlaceholderText("••••••").forEach((i) => expect(i).toHaveValue(""));
    expect(screen.getByText("Leave a password empty to keep the current one.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Add ports"), { target: { value: "9000" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByRole("alert")).toHaveTextContent("briefly disconnect and reconnect");

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls("PATCH")).toHaveLength(1));
    expect(calls("PATCH")[0][0]).toBe("/tunnels/1");
    expect((calls("PATCH")[0][1] as { body: unknown }).body).toEqual({ ports: [443, 8443, 9000] });
  });

  it("lets a port be removed with its x", async () => {
    tunnels = [tunnel()];
    renderPage();
    await screen.findByText("de-tunnel");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    await screen.findByText("Edit de-tunnel");
    fireEvent.click(screen.getByRole("button", { name: "Remove port 443" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls("PATCH")).toHaveLength(1));
    expect((calls("PATCH")[0][1] as { body: unknown }).body).toEqual({ ports: [8443] });
  });

  it("for a detected tunnel says the ports are only for monitoring and shows no reconnect warning", async () => {
    tunnels = [tunnel({ managed: false, method: "external" })];
    renderPage();
    await screen.findByText("de-tunnel");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    await screen.findByText("Edit de-tunnel");
    expect(screen.getByText("For a detected tunnel the ports are only used for monitoring.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove port 443" }));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("disables Save until something changed", async () => {
    tunnels = [tunnel()];
    renderPage();
    await screen.findByText("de-tunnel");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    await screen.findByText("Edit de-tunnel");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    fireEvent.change(screen.getAllByRole("textbox")[0], { target: { value: "renamed" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });
});

describe("detect existing tunnel", () => {
  const found = (over: Partial<TunnelDiscoverResult> = {}): TunnelDiscoverResult => ({
    found: true,
    method: "external",
    protocol: "frp over TCP via HAProxy (IPv6 with IPv4 backup)",
    relay_host: "203.0.113.5",
    relay_addrs: ["2001:db8::5", "203.0.113.5"],
    control_port: 7000,
    ports: [443, 2053],
    interface: "",
    haproxy: true,
    frp_transport: "tcp",
    node_nic: "ens3",
    relay_nic: "eth0",
    relay_read: true,
    notes: ["Found frpc on the node"],
    ...over,
  });

  const open = async () => {
    renderPage();
    await screen.findByText("No tunnels yet. Create one and the panel will provision it end to end.");
    fireEvent.click(screen.getAllByRole("button", { name: "Detect existing tunnel" })[0]);
    await screen.findByText("Detect an existing tunnel");
  };

  // The modal renders after the page, so its button is the last one with this name.
  const modalDetect = () => screen.getAllByRole("button", { name: "Detect existing tunnel" }).slice(-1)[0];

  const fillNode = () => {
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "7" } });
    const pass = document.querySelector('input[type="password"]') as HTMLInputElement;
    fireEvent.change(pass, { target: { value: "pw" } });
  };

  it("explains up front that the panel only reads and never changes the servers", async () => {
    await open();
    expect(screen.getByText(/never changes or deletes anything on your servers/)).toBeInTheDocument();
  });

  it("posts the node login to /tunnels/discover and shows protocol, addresses, ports, HAProxy and notes", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url === "/tunnels/discover") return Promise.resolve(found());
      if (url === "/tunnels" && !opts?.method) return Promise.resolve([]);
      if (url === "/nodes") return Promise.resolve([{ id: 7, name: "de-node", address: "1.1.1.1", port: 1, api_port: 2, status: "connected", usage_coefficient: 1 }]);
      return Promise.resolve({});
    });
    await open();
    fillNode();
    fireEvent.click(modalDetect());
    const result = await screen.findByTestId("discover-result");
    expect(calls("POST")[0][0]).toBe("/tunnels/discover");
    expect((calls("POST")[0][1] as { body: unknown }).body).toEqual({
      node_id: 7,
      node_ssh_port: 22,
      node_ssh_user: "root",
      node_ssh_password: "pw",
    });
    expect(result).toHaveTextContent("frp over TCP via HAProxy (IPv6 with IPv4 backup)");
    expect(result).toHaveTextContent("2001:db8::5");
    expect(result).toHaveTextContent("203.0.113.5");
    expect(result).toHaveTextContent("443");
    expect(result).toHaveTextContent("2053");
    expect(result).toHaveTextContent("HAProxy found");
    expect(result).toHaveTextContent("Found frpc on the node");
  });

  it("asks for the relay login when the relay was not read, pre-fills the host, and detects again with it", async () => {
    const answers = [found({ relay_read: false }), found()];
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url === "/tunnels/discover") return Promise.resolve(answers.shift());
      if (url === "/tunnels" && !opts?.method) return Promise.resolve([]);
      if (url === "/nodes") return Promise.resolve([{ id: 7, name: "de-node", address: "1.1.1.1", port: 1, api_port: 2, status: "connected", usage_coefficient: 1 }]);
      return Promise.resolve({});
    });
    await open();
    fillNode();
    fireEvent.click(modalDetect());
    const login = await screen.findByTestId("relay-login");
    const host = within(login).getAllByRole("textbox")[0] as HTMLInputElement;
    expect(host.value).toBe("203.0.113.5");
    // Continue is not offered until the relay has been read
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
    fireEvent.change(within(login).getAllByRole("textbox")[1], { target: { value: "relayuser" } });
    fireEvent.change(login.querySelector('input[type="password"]') as HTMLInputElement, { target: { value: "relaypw" } });
    fireEvent.click(screen.getByRole("button", { name: "Detect again" }));
    await screen.findByRole("button", { name: "Continue" });
    expect((calls("POST")[1][1] as { body: unknown }).body).toEqual({
      node_id: 7,
      node_ssh_port: 22,
      node_ssh_user: "root",
      node_ssh_password: "pw",
      relay_host: "203.0.113.5",
      relay_ssh_port: 22,
      relay_ssh_user: "relayuser",
      relay_ssh_password: "relaypw",
    });
  });

  it("names the tunnel in step 2 and adopts it with the relay login", async () => {
    const adopted = tunnel({ id: 9, name: "my-found", managed: false, method: "external" });
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url === "/tunnels/discover") return Promise.resolve(found({ relay_read: false }));
      if (url === "/tunnels/adopt") return Promise.resolve(adopted);
      if (url === "/tunnels" && !opts?.method) return Promise.resolve([]);
      if (url === "/nodes") return Promise.resolve([{ id: 7, name: "de-node", address: "1.1.1.1", port: 1, api_port: 2, status: "connected", usage_coefficient: 1 }]);
      return Promise.resolve({});
    });
    await open();
    fillNode();
    fireEvent.click(modalDetect());
    const login = await screen.findByTestId("relay-login");
    fireEvent.change(login.querySelector('input[type="password"]') as HTMLInputElement, { target: { value: "relaypw" } });
    // the same discover answer again: relay_read stays false in this stub, so
    // feed a read result for the second call
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url === "/tunnels/discover") return Promise.resolve(found());
      if (url === "/tunnels/adopt") return Promise.resolve(adopted);
      if (url === "/tunnels" && !opts?.method) return Promise.resolve([]);
      return Promise.resolve([]);
    });
    fireEvent.click(screen.getByRole("button", { name: "Detect again" }));
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));

    expect(screen.getByText(/Nothing on the node or relay is changed/)).toBeInTheDocument();
    const name = screen.getByDisplayValue("de-node-detected");
    fireEvent.change(name, { target: { value: "my-found" } });
    fireEvent.click(screen.getByRole("button", { name: "Add to panel" }));
    await waitFor(() => expect(calls("POST").some((c) => c[0] === "/tunnels/adopt")).toBe(true));
    const adopt = calls("POST").find((c) => c[0] === "/tunnels/adopt") as unknown[];
    expect((adopt[1] as { body: unknown }).body).toMatchObject({
      name: "my-found",
      node_id: 7,
      node_ssh_user: "root",
      node_ssh_password: "pw",
      relay_host: "203.0.113.5",
      relay_ssh_user: "root",
      relay_ssh_password: "relaypw",
    });
  });

  it("reports when nothing was found and does not offer to continue", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url === "/tunnels/discover") return Promise.resolve(found({ found: false, notes: ["no frpc, no gre"], ports: [], relay_addrs: [] }));
      if (url === "/tunnels" && !opts?.method) return Promise.resolve([]);
      return Promise.resolve([{ id: 7, name: "de-node", address: "1.1.1.1", port: 1, api_port: 2, status: "connected", usage_coefficient: 1 }]);
    });
    await open();
    fillNode();
    fireEvent.click(modalDetect());
    expect(await screen.findByText("No tunnel was found on this node.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
  });
});
