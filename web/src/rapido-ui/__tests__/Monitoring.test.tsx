import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import i18n from "locales/i18n";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { MonitoringHistoryPoint, MonitoringHost, MonitoringTunnel } from "types/Monitoring";
import { Tunnel } from "types/Tunnel";

const state: { hosts: MonitoringHost[]; tunnels: Tunnel[]; history: Record<string, MonitoringHistoryPoint[]> } = {
  hosts: [],
  tunnels: [],
  history: {},
};

vi.mock("hooks/useMonitoringQuery", () => ({
  useMonitoringQuery: () => ({
    data: { hosts: state.hosts, generated_at: "2026-09-25T00:00:00Z" },
    isLoading: false,
    isError: false,
  }),
  useMonitoringHistoryQuery: (id: number | null) => ({ data: state.history[String(id)], isLoading: false }),
  useFleetHistoryQueries: (ids: (number | null)[]) =>
    ids.map((id) => ({ data: state.history[String(id)], dataUpdatedAt: state.history[String(id)] ? 1 : 0 })),
}));
vi.mock("hooks/useNodesQuery", () => ({
  useNodesQuery: () => ({ data: [{ id: 1, name: "de", ssh_known: false, listen_ports: [443, 8443] }] }),
  useNodesUsageQuery: () => ({ data: undefined }),
  useRestartNodeMutation: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock("hooks/useSystemStatsQuery", () => ({
  useSystemStatsQuery: () => ({ data: { online_users: 3, total_user: 10 } }),
}));
vi.mock("hooks/useTunnelsQuery", () => ({
  useTunnelsQuery: () => ({ data: state.tunnels }),
  useTunnelSamplesQueries: (ids: number[]) => ids.map(() => ({ data: undefined, dataUpdatedAt: 0 })),
}));

import { Monitoring } from "../Monitoring";
import { buildSeries, fleetTotals } from "../FleetBoard";

beforeAll(async () => {
  await i18n.changeLanguage("en");
});
beforeEach(() => {
  state.hosts = [];
  state.tunnels = [];
  state.history = {};
});

const tunnel = (o: Partial<MonitoringTunnel> = {}): MonitoringTunnel => ({
  name: "wg0",
  up: true,
  present: true,
  rx_bytes: 1024 * 1024,
  tx_bytes: 2048,
  last_handshake: 0,
  handshake_age_seconds: 12,
  probe_ms: 23.4,
  fallback_active: false,
  ...o,
});

const host = (name: string, tunnels: MonitoringTunnel[] | null, o: Partial<MonitoringHost> = {}): MonitoringHost => ({
  node_id: 1,
  name,
  address: "10.0.0.1",
  reachable: true,
  has_metrics: true,
  collected_at: "2026-09-25T00:00:00Z",
  stale: false,
  cpu_percent: 10,
  mem_percent: 20,
  disk_percent: 30,
  rx_rate: 0,
  tx_rate: 0,
  connections: 5,
  tunnels_up: tunnels ? tunnels.filter((t) => t.up && t.present).length : null,
  tunnels_total: tunnels ? tunnels.length : null,
  tunnels,
  healthy: true,
  ...o,
});

// A node card is the rounded card whose title span carries the node's name.
const card = (name: string) =>
  screen.getAllByText(name, { selector: "span.truncate" }).map((n) => n.closest("div.rounded-xl2") as HTMLElement)[0];

describe("Monitoring layout", () => {
  it("puts the fleet overview - cluster, board and the over-time chart in ONE panel - above the node cards", () => {
    state.hosts = [host("de", null)];
    render(<Monitoring />);
    const fleet = screen.getByRole("region", { name: "Fleet overview" });
    expect(within(fleet).getByRole("table", { name: "Fleet board" })).toBeInTheDocument();
    expect(within(fleet).getByText("Over time")).toBeInTheDocument();
    expect(within(fleet).getByRole("radiogroup", { name: "Metric" })).toBeInTheDocument();
    expect(within(fleet).getByRole("radiogroup", { name: "Scope" })).toBeInTheDocument();
    // the node card comes after it in the document
    const nodeCard = card("de");
    expect(fleet.compareDocumentPosition(nodeCard) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // and the chart is not a card of its own
    expect(within(fleet).getByText("Over time").closest("div.rounded-xl2")).toBe(fleet.querySelector("div.rounded-xl2"));
  });

  it("lists every node and tunnel as a row of the board", () => {
    state.hosts = [host("de", null), host("nl", null, { node_id: 2 })];
    state.tunnels = [{ id: 7, name: "relay-1", node_id: 1, status: "active", health: { up: true }, metrics: null } as unknown as Tunnel];
    render(<Monitoring />);
    const board = within(screen.getByRole("table", { name: "Fleet board" }));
    expect(board.getAllByRole("row")).toHaveLength(1 + 3);
    expect(board.getByText("relay-1")).toBeInTheDocument();
    expect(board.getAllByText("Node")).toHaveLength(2);
    expect(board.getByText("Tunnel")).toBeInTheDocument();
  });

  it("disables a metric no series exists for rather than drawing one from nothing", () => {
    state.hosts = [host("de", null)];
    render(<Monitoring />);
    // no history was recorded, so every metric is unavailable
    expect(screen.getByRole("radio", { name: "Disk" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "CPU" })).toBeDisabled();
    expect(screen.getByText("No history recorded yet")).toBeInTheDocument();
  });

  it("offers the metrics that have history, and never Disk for nodes (history carries none)", () => {
    state.hosts = [host("de", null)];
    state.history["1"] = [
      { t: "2026-09-25T00:00:00Z", cpu: 10, mem: 20, rx: 100, tx: 50, conns: 5 },
      { t: "2026-09-25T00:05:00Z", cpu: 30, mem: 25, rx: 200, tx: 70, conns: 9 },
    ];
    render(<Monitoring />);
    expect(screen.getByRole("radio", { name: "CPU" })).toBeEnabled();
    expect(screen.getByRole("radio", { name: "RAM" })).toBeEnabled();
    expect(screen.getByRole("radio", { name: "Traffic" })).toBeEnabled();
    expect(screen.getByRole("radio", { name: "Disk" })).toBeDisabled();
  });

  it("has a Restart node action on a node card but not on the panel's own card", () => {
    state.hosts = [host("de", null), host("Panel", null, { node_id: null, address: null })];
    render(<Monitoring />);
    expect(within(card("de")).getByRole("button", { name: "Restart node" })).toBeInTheDocument();
    expect(within(card("Panel")).queryByRole("button", { name: "Restart node" })).toBeNull();
  });

  it("shows the node's listen ports as chips in its footer", () => {
    state.hosts = [host("de", null)];
    render(<Monitoring />);
    expect(within(card("de")).getByText("8443")).toBeInTheDocument();
    expect(within(card("de")).getByText("Listen ports (2)")).toBeInTheDocument();
  });
});

describe("fleet maths", () => {
  it("averages what hosts report, sums connections and rates (bytes/s to bits/s), and skips silent hosts", () => {
    const t = fleetTotals(
      [
        host("a", null, { cpu_percent: 20, mem_percent: 40, rx_rate: 1000, tx_rate: 500, connections: 5 }),
        host("b", null, { node_id: 2, cpu_percent: 40, mem_percent: 60, rx_rate: 1000, tx_rate: 500, connections: 7 }),
        host("dead", null, { node_id: 3, reachable: false, cpu_percent: 99, rx_rate: 99999, connections: 999 }),
      ],
      []
    );
    expect(t.nodes).toBe(2);
    expect(t.nodesTotal).toBe(3);
    expect(t.cpu).toBe(30);
    expect(t.mem).toBe(50);
    expect(t.rx).toBe(16000);
    expect(t.tx).toBe(8000);
    expect(t.connections).toBe(12);
  });

  it("reports null, not zero, when nothing reported", () => {
    const t = fleetTotals([host("dead", null, { reachable: false })], []);
    expect(t.cpu).toBeNull();
    expect(t.rx).toBeNull();
    expect(t.tunnelsReporting).toBe(0);
  });

  it("builds a series per host with history, converting node traffic to bits per second", () => {
    const hosts = [host("a", null), host("b", null, { node_id: 2 })];
    const history = new Map<number | null, MonitoringHistoryPoint[] | undefined>([
      [1, [{ t: "2026-09-25T00:00:00Z", cpu: 1, mem: 2, rx: 100, tx: 50, conns: 3 }, { t: "2026-09-25T00:05:00Z", cpu: 4, mem: 5, rx: 100, tx: 50, conns: 6 }]],
      [2, undefined],
    ]);
    const s = buildSeries({ metric: "traffic", scope: "nodes", hosts, history, tunnels: [], samples: new Map() });
    expect(s).toHaveLength(1);
    expect(s[0].label).toBe("a");
    expect(s[0].points[0].v).toBe(1200);
    expect(buildSeries({ metric: "disk", scope: "all", hosts, history, tunnels: [], samples: new Map() })).toEqual([]);
    expect(buildSeries({ metric: "cpu", scope: "tunnels", hosts, history, tunnels: [], samples: new Map() })).toEqual([]);
  });

  it("uses the same three data colours first: blue, violet, green", () => {
    const hosts = [host("a", null), host("b", null, { node_id: 2 }), host("c", null, { node_id: 3 })];
    const pts = [{ t: "2026-09-25T00:00:00Z", cpu: 1, mem: 2, rx: 1, tx: 1, conns: 3 }, { t: "2026-09-25T00:05:00Z", cpu: 4, mem: 5, rx: 1, tx: 1, conns: 6 }];
    const history = new Map<number | null, MonitoringHistoryPoint[] | undefined>([[1, pts], [2, pts], [3, pts]]);
    const s = buildSeries({ metric: "cpu", scope: "nodes", hosts, history, tunnels: [], samples: new Map() });
    expect(s.map((x) => x.color)).toEqual(["rgb(var(--rp-accent))", "rgb(var(--rp-violet))", "rgb(var(--rp-good))"]);
  });
});

describe("Monitoring links", () => {
  it("shows every WireGuard exit as an indicator light with its state, handshake age, latency and traffic", () => {
    state.hosts = [host("de", [tunnel({ name: "wg-de" })])];
    render(<Monitoring />);
    const c = within(card("de"));
    const light = c.getByText("wg-de").closest("li") as HTMLElement;
    expect(light).toHaveAttribute("data-state", "up");
    const l = within(light);
    expect(l.getByText(/Up/)).toBeInTheDocument();
    expect(l.getByText(/12 s ago/)).toBeInTheDocument();
    expect(l.getByText(/23 ms/)).toBeInTheDocument();
    expect(l.getByText(/↓ 1 MB/)).toBeInTheDocument();
    expect(l.getByText(/↑ 2 KB/)).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("orders down and missing links before healthy ones, then by name", () => {
    state.hosts = [
      host("de", [
        tunnel({ name: "wg-a" }),
        tunnel({ name: "wg-z", up: false }),
        tunnel({ name: "wg-m", up: false, present: false }),
      ]),
    ];
    render(<Monitoring />);
    const names = within(card("de"))
      .getAllByRole("listitem")
      .map((li) => li.querySelector("span.truncate")?.textContent);
    expect(names).toEqual(["wg-m", "wg-z", "wg-a"]);
  });

  it("lights a missing interface, a plain down exit and a down exit on the direct fallback differently", () => {
    state.hosts = [
      host("de", [
        tunnel({ name: "gone", up: false, present: false }),
        tunnel({ name: "dead", up: false }),
        tunnel({ name: "covered", up: false, fallback_active: true }),
      ]),
    ];
    render(<Monitoring />);
    const c = within(card("de"));
    const state_ = (name: string) => (c.getByText(name).closest("li") as HTMLElement).getAttribute("data-state");
    expect(state_("gone")).toBe("down");
    expect(state_("dead")).toBe("down");
    expect(state_("covered")).toBe("degraded");
    expect(c.getByText(/Interface missing/)).toBeInTheDocument();
    expect(c.getByText(/Down - using direct fallback/)).toBeInTheDocument();
    // A missing interface has no handshake or traffic to report.
    expect(c.getAllByText(/Last handshake/)).toHaveLength(2);
  });

  it("says never when there has been no handshake, and shows the error as text and tooltip", () => {
    state.hosts = [
      host("de", [tunnel({ up: false, handshake_age_seconds: null, last_handshake: 0, probe_ms: null, error: "probe timed out" })]),
    ];
    render(<Monitoring />);
    const c = within(card("de"));
    expect(c.getByText(/never/)).toBeInTheDocument();
    expect(c.getByText("probe timed out")).toBeInTheDocument();
    expect(c.getByText("wg0").closest("li")).toHaveAttribute("title", "probe timed out");
    expect(c.queryByText(/ms$/)).not.toBeInTheDocument();
  });

  it("humanises minutes", () => {
    state.hosts = [host("de", [tunnel({ handshake_age_seconds: 200 })])];
    render(<Monitoring />);
    expect(screen.getByText(/3 min ago/)).toBeInTheDocument();
  });

  it("shows no links zone for a host with no tunnels, and keeps the bare N/M for one without detail", () => {
    state.hosts = [
      host("Panel", null, { node_id: null, address: null }),
      host("old", null, { tunnels_up: 1, tunnels_total: 2 }),
    ];
    render(<Monitoring />);
    expect(within(card("Panel")).queryByText("Links")).not.toBeInTheDocument();
    expect(within(card("old")).queryAllByRole("listitem")).toHaveLength(0);
    expect(within(card("old")).getByText("WireGuard tunnels 1/2")).toBeInTheDocument();
  });

  describe("fleet warning", () => {
    it("lists node: tunnel for every tunnel that is not up, in red when one has no fallback", () => {
      state.hosts = [
        host("de", [tunnel({ name: "wg1", up: false }), tunnel({ name: "wg2" })]),
        host("nl", [tunnel({ name: "wg9", up: false, fallback_active: true })], { node_id: 2 }),
      ];
      render(<Monitoring />);
      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent("Tunnels not up: 2 of 3");
      expect(within(alert).getByText("de: wg1")).toBeInTheDocument();
      expect(within(alert).getByText("nl: wg9")).toBeInTheDocument();
      expect(alert.className).toMatch(/red/);
      expect(alert).toHaveTextContent("1 of them are carried by the direct fallback");
      expect(alert).toHaveTextContent("1 have no fallback");
    });

    it("is amber when every down tunnel is covered by the direct fallback", () => {
      state.hosts = [host("de", [tunnel({ up: false, fallback_active: true })])];
      render(<Monitoring />);
      const alert = screen.getByRole("alert");
      expect(alert.className).toMatch(/amber/);
      expect(alert).not.toHaveTextContent("have no fallback");
    });

    it("collapses a long list behind a show-all toggle", async () => {
      state.hosts = [host("de", Array.from({ length: 9 }, (_, i) => tunnel({ name: `wg${i}`, up: false })))];
      render(<Monitoring />);
      const alert = screen.getByRole("alert");
      expect(within(alert).getAllByRole("listitem")).toHaveLength(6);
      await userEvent.click(within(alert).getByRole("button", { name: "Show all (9)" }));
      expect(within(alert).getAllByRole("listitem")).toHaveLength(9);
      await userEvent.click(within(alert).getByRole("button", { name: "Show fewer" }));
      expect(within(alert).getAllByRole("listitem")).toHaveLength(6);
    });
  });
});
