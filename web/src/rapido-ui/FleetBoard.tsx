import { FC, useMemo, useState } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { useFleetHistoryQueries, useMonitoringQuery } from "hooks/useMonitoringQuery";
import { useNodesUsageQuery } from "hooks/useNodesQuery";
import { useTunnelSamplesQueries, useTunnelsQuery } from "hooks/useTunnelsQuery";
import { MonitoringHistoryPoint, MonitoringHost } from "types/Monitoring";
import { Tunnel, TunnelSamples } from "types/Tunnel";
import { hostDisplayState } from "utils/monitoringHost";
import { formatBitrateText, isNumber } from "utils/tunnelMetrics";
import { Card, CardSubtitle, CardTitle } from "./Card";
import { Cluster, ClusterZone } from "./Cluster";
import { DataTable, DataTableColumn } from "./DataTable";
import { NicZone } from "./NicZone";
import { ResourceMeter } from "./ResourceMeter";
import { Segmented } from "./Segmented";
import { SegmentBar, TrendBars } from "./Segments";
import { TimeSeriesChart, ChartSeries } from "./TimeSeriesChart";
import { GAUGE_BAD, GAUGE_CYAN, GAUGE_GOOD, GAUGE_TEAL, GAUGE_VIOLET, GAUGE_WARN, TYPE, UNLIT, glowShadow, solidZones } from "./instrument";
import { useAutoMax } from "./useAutoMax";
import { useRollingSeries } from "./useRollingSeries";

// The fleet overview: one board for every node and every tunnel. An aggregate
// instrument cluster on top (the fleet's traffic as dual rings, the averages as
// rings), a compact board of rows, and one time-series chart you can switch
// between metrics and between nodes, tunnels or both. Everything is read from
// /monitoring, /monitoring/history, /nodes/usage, /system and /tunnels (and the
// tunnel samples endpoint when the backend has it); a series or zone with no
// data is left out, never drawn from zeros.

export type Metric = "traffic" | "cpu" | "ram" | "disk" | "connections";
export type Scope = "nodes" | "tunnels" | "all";

const bps = (bytesPerSecond: number | null | undefined) => (isNumber(bytesPerSecond) ? bytesPerSecond * 8 : null);
const sum2 = (a: number | null, b: number | null) => (a === null && b === null ? null : (a ?? 0) + (b ?? 0));

/** The aggregate numbers the fleet cluster shows. A mean is over the hosts that reported it. */
export const fleetTotals = (hosts: MonitoringHost[], tunnels: Tunnel[]) => {
  const reporting = hosts.filter((h) => h.reachable);
  const mean = (pick: (h: MonitoringHost) => number | null) => {
    const v = reporting.map(pick).filter(isNumber);
    return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null;
  };
  const total = (pick: (h: MonitoringHost) => number | null) => {
    const v = reporting.map(pick).filter(isNumber);
    return v.length ? v.reduce((a, b) => a + b, 0) : null;
  };
  const tm = tunnels.map((t) => t.metrics).filter((m): m is NonNullable<Tunnel["metrics"]> => !!m);
  const tsum = (pick: (m: NonNullable<Tunnel["metrics"]>) => number | undefined) => {
    const v = tm.map(pick).filter(isNumber);
    return v.length ? v.reduce((a, b) => a + b, 0) : null;
  };
  return {
    nodes: reporting.length,
    nodesTotal: hosts.length,
    rx: (() => {
      const v = total((h) => h.rx_rate);
      return v === null ? null : v * 8;
    })(),
    tx: (() => {
      const v = total((h) => h.tx_rate);
      return v === null ? null : v * 8;
    })(),
    cpu: mean((h) => h.cpu_percent),
    mem: mean((h) => h.mem_percent),
    disk: mean((h) => h.disk_percent),
    connections: total((h) => h.connections),
    tunnelsRx: tsum((m) => m.nic_rx_bps),
    tunnelsTx: tsum((m) => m.nic_tx_bps),
    tunnelsRxBytes: tsum((m) => m.nic_rx_bytes),
    tunnelsTxBytes: tsum((m) => m.nic_tx_bytes),
    tunnelsReporting: tm.length,
  };
};

// The data palette: the blue accent, violet and the healthy green first, then lighter tints of the same three.
const SERIES_COLORS = [GAUGE_CYAN, GAUGE_VIOLET, GAUGE_GOOD, "rgb(125 211 252)", "rgb(196 181 253)", "rgb(134 239 172)", "rgb(56 189 248)", "rgb(167 139 250)"];

type HistoryByNode = Map<number | null, MonitoringHistoryPoint[] | undefined>;
type SamplesByTunnel = Map<number, TunnelSamples | undefined>;

const ts = (iso: string) => Date.parse(iso);

/**
 * The chart series for a metric and scope. Node history has no disk; tunnel
 * samples have no memory or disk - those combinations give no series (and the
 * metric button is disabled), rather than a made-up line.
 */
export const buildSeries = (args: {
  metric: Metric;
  scope: Scope;
  hosts: MonitoringHost[];
  history: HistoryByNode;
  tunnels: Tunnel[];
  samples: SamplesByTunnel;
}): ChartSeries[] => {
  const { metric, scope, hosts, history, tunnels, samples } = args;
  const out: Omit<ChartSeries, "color">[] = [];
  if (scope !== "tunnels") {
    for (const h of hosts) {
      const pts = history.get(h.node_id);
      if (!pts || pts.length < 2) continue;
      const pick = (p: MonitoringHistoryPoint): number | null => {
        switch (metric) {
          case "traffic":
            return sum2(bps(p.rx), bps(p.tx));
          case "cpu":
            return p.cpu;
          case "ram":
            return p.mem;
          case "connections":
            return p.conns;
          default:
            return null;
        }
      };
      const points = pts.map((p) => ({ t: ts(p.t), v: pick(p) }));
      if (points.some((p) => isNumber(p.v))) out.push({ key: `n${h.node_id ?? "panel"}`, label: h.name, points });
    }
  }
  if (scope !== "nodes") {
    for (const tn of tunnels) {
      const s = samples.get(tn.id);
      if (!s || !Array.isArray(s.points) || s.points.length < 2) continue;
      const pick = (p: TunnelSamples["points"][number]): number | null => {
        switch (metric) {
          case "traffic":
            return sum2(p.relay_rx_bps, p.relay_tx_bps);
          case "cpu":
            return p.cpu;
          case "connections":
            return p.connections;
          default:
            return null;
        }
      };
      const points = s.points.map((p) => ({ t: ts(p.t), v: pick(p) }));
      if (points.some((p) => isNumber(p.v))) out.push({ key: `t${tn.id}`, label: tn.name, points });
    }
  }
  return out.map((s, i) => ({ ...s, color: SERIES_COLORS[i % SERIES_COLORS.length] }));
};

// ---------------------------------------------------------------------------

const useFleetData = () => {
  const { data: snap, isLoading, isError } = useMonitoringQuery();
  const { data: tunnels } = useTunnelsQuery();
  const hosts = useMemo(() => snap?.hosts ?? [], [snap]);
  const tunnelList = useMemo(() => tunnels ?? [], [tunnels]);
  const histQs = useFleetHistoryQueries(hosts.map((h) => h.node_id));
  const sampleQs = useTunnelSamplesQueries(tunnelList.map((t) => t.id));
  // One string dependency stands for "some query changed": a dependency list whose
  // length followed the number of hosts would change size between renders.
  const histVersion = histQs.map((q) => q.dataUpdatedAt).join(",");
  const sampleVersion = sampleQs.map((q) => q.dataUpdatedAt).join(",");
  const history: HistoryByNode = useMemo(() => {
    const m: HistoryByNode = new Map();
    hosts.forEach((h, i) => m.set(h.node_id, histQs[i]?.data));
    return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hosts, histVersion]);
  const samples: SamplesByTunnel = useMemo(() => {
    const m: SamplesByTunnel = new Map();
    tunnelList.forEach((t, i) => m.set(t.id, sampleQs[i]?.data));
    return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tunnelList, sampleVersion]);
  return { snap, hosts, tunnels: tunnelList, history, samples, isLoading, isError };
};

/** The aggregate cluster: fleet traffic in and out, the same for the tunnels' relays, and the averages. */
export type FleetUsers = { online: number; total: number };

export const FleetCluster: FC<{ hosts: MonitoringHost[]; tunnels: Tunnel[]; stamp: string; users?: FleetUsers }> = ({ hosts, tunnels, stamp, users }) => {
  const { t } = useTranslation();
  const L = (k: string, o?: Record<string, unknown>) => String(o ? t(k, o) : t(k));
  const totals = useMemo(() => fleetTotals(hosts, tunnels), [hosts, tunnels]);
  const { data: usage } = useNodesUsageQuery();
  const conn = useAutoMax(totals.connections, 100);
  const nodeBytes = usage?.usages.length
    ? { rx: usage.usages.reduce((a, u) => a + (u.downlink || 0), 0), tx: usage.usages.reduce((a, u) => a + (u.uplink || 0), 0) }
    : null;
  const avg = L("rapido.monitoring.average");
  const n = totals.nodes;

  return (
    <Cluster aria-label={L("rapido.monitoring.fleetLabel")}>
      {(totals.rx !== null || totals.tx !== null) && (
        <NicZone
          title={L("rapido.monitoring.fleetNodesTraffic")}
          rx={totals.rx}
          tx={totals.tx}
          rxBytes={nodeBytes?.rx}
          txBytes={nodeBytes?.tx}
          seriesKey="fleet:nodes"
          stamp={stamp}
          ariaLabel={L("rapido.monitoring.fleetNodesTraffic")}
        />
      )}
      {totals.tunnelsReporting > 0 && (
        <NicZone
          title={L("rapido.monitoring.fleetTunnelsTraffic")}
          rx={totals.tunnelsRx}
          tx={totals.tunnelsTx}
          rxBytes={totals.tunnelsRxBytes}
          txBytes={totals.tunnelsTxBytes}
          seriesKey="fleet:tunnels"
          stamp={stamp}
          ariaLabel={L("rapido.monitoring.fleetTunnelsTraffic")}
        />
      )}
      <ClusterZone title={L("rapido.tunnels.resources")}>
        <div className="grid justify-items-center gap-x-3 gap-y-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(5.6rem, 1fr))" }}>
          <ResourceMeter value={totals.cpu} max={100} unit="%" size={92} label={L("rapido.monitoring.cpu")} sublabel={`${avg} · ${n}`} />
          <ResourceMeter value={totals.mem} max={100} unit="%" size={92} label={L("rapido.monitoring.memory")} sublabel={`${avg} · ${n}`} />
          <ResourceMeter value={totals.disk} max={100} unit="%" size={92} label={L("rapido.monitoring.disk")} sublabel={`${avg} · ${n}`} />
          <ResourceMeter value={totals.connections} max={conn.max} size={92} label={L("rapido.monitoring.connections")} zones={solidZones(GAUGE_CYAN)} peak={conn.peak > 0 ? conn.peak : null} />
          {users && (
            <ResourceMeter
              value={users.online}
              max={Math.max(users.total, users.online, 1)}
              size={92}
              label={L("rapido.monitoring.activeUsers")}
              sublabel={`${users.online} / ${users.total}`}
              zones={solidZones(GAUGE_VIOLET)}
            />
          )}
        </div>
      </ClusterZone>
    </Cluster>
  );
};

// ---------------------------------------------------------------------------

type Row = {
  key: string;
  kind: "node" | "tunnel";
  name: string;
  state: "up" | "degraded" | "down" | "unknown";
  rx: number | null;
  tx: number | null;
  cpu: number | null;
  mem: number | null;
  disk: number | null;
  conns: number | null;
  trend: (number | null)[] | undefined;
  stamp: string;
};

const stateColor = { up: GAUGE_GOOD, degraded: GAUGE_WARN, down: GAUGE_BAD, unknown: UNLIT } as const;

const MiniBar: FC<{ value: number | null }> = ({ value }) => (
  <div className="flex w-28 items-center gap-2" dir="ltr">
    <SegmentBar value={value} max={100} count={10} className="h-1.5" />
    <span className="w-9 text-end text-xs tabular-nums text-rapido-text">{isNumber(value) ? `${Math.round(value)}%` : "—"}</span>
  </div>
);

const RowTrend: FC<{ row: Row }> = ({ row }) => {
  const rolling = useRollingSeries(`fleet:row:${row.key}`, sum2(row.rx, row.tx), row.stamp, 30);
  const values = row.trend && row.trend.length > 1 ? row.trend : rolling;
  return <TrendBars values={values} bars={20} height={18} zones={solidZones(GAUGE_CYAN)} className="w-24" ariaLabel={`${row.name} trend`} />;
};

export const FleetTable: FC<{
  hosts: MonitoringHost[];
  tunnels: Tunnel[];
  history: HistoryByNode;
  stamp: string;
}> = ({ hosts, tunnels, history, stamp }) => {
  const { t } = useTranslation();
  const L = (k: string) => String(t(k));
  const rows: Row[] = [
    ...hosts.map((h): Row => {
      const st = hostDisplayState(h);
      const hist = history.get(h.node_id);
      return {
        key: `n${h.node_id ?? "panel"}`,
        kind: "node",
        name: h.name,
        state: st === "healthy" ? "up" : st === "stale" ? "degraded" : st === "unhealthy" ? "down" : "unknown",
        rx: bps(h.rx_rate),
        tx: bps(h.tx_rate),
        cpu: h.cpu_percent,
        mem: h.mem_percent,
        disk: h.disk_percent,
        conns: h.connections,
        trend: hist && hist.length > 1 ? hist.map((p) => sum2(bps(p.rx), bps(p.tx))) : undefined,
        stamp: h.collected_at ?? "none",
      };
    }),
    ...tunnels.map((tn): Row => {
      const m = tn.metrics;
      const up = tn.health ? tn.health.up : null;
      return {
        key: `t${tn.id}`,
        kind: "tunnel",
        name: tn.name,
        state: tn.status !== "active" ? "degraded" : up === null ? "unknown" : up ? "up" : "down",
        rx: m && isNumber(m.nic_rx_bps) ? m.nic_rx_bps : null,
        tx: m && isNumber(m.nic_tx_bps) ? m.nic_tx_bps : null,
        cpu: m ? m.cpu_percent : null,
        mem: m && m.mem_total_mb > 0 ? (m.mem_used_mb / m.mem_total_mb) * 100 : null,
        disk: m && m.disk_total_gb > 0 ? (m.disk_used_gb / m.disk_total_gb) * 100 : null,
        conns: m ? m.connections : null,
        trend: undefined,
        stamp: m?.checked_at ?? "none",
      };
    }),
  ];

  const columns: DataTableColumn<Row>[] = [
    {
      key: "name",
      header: L("rapido.monitoring.colName"),
      cell: (r) => (
        <span className="flex items-center gap-2.5">
          <span aria-hidden="true" data-state={r.state} className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: stateColor[r.state], boxShadow: r.state === "unknown" ? undefined : glowShadow(stateColor[r.state]) }} />
          <span className="font-medium" dir="ltr">
            {r.name}
          </span>
          <span className="rounded-md bg-rapido-raised px-1.5 py-0.5 text-[10px] font-semibold uppercase text-rapido-muted">
            {L(r.kind === "node" ? "rapido.monitoring.kindNode" : "rapido.monitoring.kindTunnel")}
          </span>
        </span>
      ),
    },
    {
      key: "traffic",
      header: L("rapido.monitoring.colTraffic"),
      cell: (r) => (
        <span className="whitespace-nowrap text-xs tabular-nums" dir="ltr">
          <span className="text-rapido-accent">↓ {isNumber(r.rx) ? formatBitrateText(r.rx) : "—"}</span>{" "}
          <span className="text-rapido-violet">↑ {isNumber(r.tx) ? formatBitrateText(r.tx) : "—"}</span>
        </span>
      ),
    },
    { key: "cpu", header: L("rapido.monitoring.cpu"), cell: (r) => <MiniBar value={r.cpu} /> },
    { key: "mem", header: L("rapido.monitoring.memory"), cell: (r) => <MiniBar value={r.mem} /> },
    { key: "disk", header: L("rapido.monitoring.disk"), cell: (r) => <MiniBar value={r.disk} /> },
    {
      key: "conns",
      header: L("rapido.monitoring.connections"),
      align: "end",
      cell: (r) => (
        <span className={classNames(TYPE.readout, "text-sm")} dir="ltr">
          {isNumber(r.conns) ? r.conns : "—"}
        </span>
      ),
    },
    { key: "trend", header: L("rapido.monitoring.colTrend"), cell: (r) => <RowTrend row={r} /> },
  ];

  return <DataTable columns={columns} rows={rows} rowKey={(r) => r.key} aria-label={L("rapido.monitoring.boardTitle")} maxHeight="36rem" empty={L("rapido.monitoring.loading")} />;
};

// ---------------------------------------------------------------------------

export const FleetChart: FC<{
  hosts: MonitoringHost[];
  tunnels: Tunnel[];
  history: HistoryByNode;
  samples: SamplesByTunnel;
}> = ({ hosts, tunnels, history, samples }) => {
  const { t, i18n } = useTranslation();
  const L = (k: string) => String(t(k));
  const [metric, setMetric] = useState<Metric>("traffic");
  const [scope, setScope] = useState<Scope>("all");
  const [hidden, setHidden] = useState<ReadonlySet<string>>(new Set());

  const all = useMemo(() => buildSeries({ metric, scope, hosts, history, tunnels, samples }), [metric, scope, hosts, history, tunnels, samples]);
  const available = (m: Metric) => buildSeries({ metric: m, scope, hosts, history, tunnels, samples }).length > 0;
  const series = all.filter((s) => !hidden.has(s.key));

  const metrics: Metric[] = ["traffic", "cpu", "ram", "disk", "connections"];
  const format = (v: number) =>
    metric === "traffic" ? formatBitrateText(v) : metric === "connections" ? String(Math.round(v)) : `${Math.round(v)}%`;
  const formatTime = (ms: number) =>
    new Date(ms).toLocaleTimeString(i18n.language, { hour: "2-digit", minute: "2-digit" });
  const toggle = (key: string) =>
    setHidden((h) => {
      const next = new Set(h);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  return (
    <div className="flex flex-col gap-3 border-t border-rapido-border/60 pt-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <CardTitle>{L("rapido.monitoring.chartTitle")}</CardTitle>
          <CardSubtitle>{L("rapido.monitoring.chartSubtitle")}</CardSubtitle>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <Segmented
            label={L("rapido.monitoring.scopeLabel")}
            value={scope}
            onChange={setScope}
            options={(["all", "nodes", "tunnels"] as Scope[]).map((v) => ({ value: v, label: L(`rapido.monitoring.scope.${v}`) }))}
          />
        </div>
      </div>
      <div role="radiogroup" aria-label={L("rapido.monitoring.metric")} className="flex flex-wrap gap-1.5">
        {metrics.map((m) => {
          const ok = available(m);
          const on = metric === m;
          return (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={on}
              disabled={!ok}
              title={ok ? undefined : L("rapido.monitoring.metricUnavailable")}
              onClick={() => setMetric(m)}
              className={classNames(
                "rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-40",
                on ? "border-rapido-accent bg-rapido-accent/15 text-rapido-accent shadow-rp-glow" : "border-rapido-border text-rapido-muted hover:border-rapido-accent/50 hover:text-rapido-text"
              )}
            >
              {L(`rapido.monitoring.metric.${m}`)}
            </button>
          );
        })}
      </div>
      <TimeSeriesChart
        series={series}
        format={format}
        formatTime={formatTime}
        ariaLabel={`${L(`rapido.monitoring.metric.${metric}`)} - ${L(`rapido.monitoring.scope.${scope}`)}`}
        empty={L("rapido.monitoring.noHistory")}
      />
      {all.length > 0 && (
        <ul className="flex flex-wrap gap-1.5" aria-label={L("rapido.monitoring.legend")}>
          {all.map((s) => {
            const off = hidden.has(s.key);
            return (
              <li key={s.key}>
                <button
                  type="button"
                  aria-pressed={!off}
                  onClick={() => toggle(s.key)}
                  className={classNames(
                    "flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs transition-opacity",
                    off ? "border-rapido-border text-rapido-muted opacity-50" : "border-rapido-border/70 text-rapido-text"
                  )}
                >
                  <span aria-hidden="true" className="h-2 w-2 rounded-full" style={{ background: s.color }} />
                  <span dir="ltr">{s.label}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
};

// ---------------------------------------------------------------------------

/** The fleet overview: ONE panel - the aggregate cluster, the board, and the over-time chart. */
export const FleetOverview: FC<{ users?: FleetUsers }> = ({ users }) => {
  const { t } = useTranslation();
  const L = (k: string) => String(t(k));
  const { snap, hosts, tunnels, history, samples, isLoading } = useFleetData();
  const stamp = snap?.generated_at ?? "none";
  const nodesUp = hosts.filter((h) => h.reachable).length;
  const tunnelsUp = tunnels.filter((tn) => tn.status === "active" && tn.health?.up).length;

  if (isLoading) return <div className="text-sm text-rapido-muted">{L("rapido.monitoring.loading")}</div>;
  if (hosts.length === 0 && tunnels.length === 0) return null;

  return (
    <section className="flex flex-col gap-4" aria-label={L("rapido.monitoring.fleetTitle")}>
      <Card className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <CardTitle className="text-base">{L("rapido.monitoring.fleetTitle")}</CardTitle>
            <CardSubtitle>{L("rapido.monitoring.fleetSubtitle")}</CardSubtitle>
          </div>
          <div className="flex flex-wrap gap-2 text-xs text-rapido-muted" dir="ltr">
            <span className="rounded-full border border-rapido-border/70 px-2.5 py-1">
              {L("rapido.monitoring.kindNode")} <b className="tabular-nums text-rapido-text">{nodesUp}/{hosts.length}</b>
            </span>
            <span className="rounded-full border border-rapido-border/70 px-2.5 py-1">
              {L("rapido.monitoring.kindTunnel")} <b className="tabular-nums text-rapido-text">{tunnelsUp}/{tunnels.length}</b>
            </span>
          </div>
        </div>
        <FleetCluster hosts={hosts} tunnels={tunnels} stamp={stamp} users={users} />
        <FleetTable hosts={hosts} tunnels={tunnels} history={history} stamp={stamp} />
        <FleetChart hosts={hosts} tunnels={tunnels} history={history} samples={samples} />
      </Card>
    </section>
  );
};


/** The aggregate cluster alone, as a card - for the Overview page. */
export const FleetClusterCard: FC<{ title: string; subtitle?: string; users?: FleetUsers }> = ({ title, subtitle, users }) => {
  const { data: snap } = useMonitoringQuery();
  const { data: tunnels } = useTunnelsQuery();
  const hosts = useMemo(() => snap?.hosts ?? [], [snap]);
  const tunnelList = useMemo(() => tunnels ?? [], [tunnels]);
  if (hosts.length === 0) return null;
  return (
    <Card className="flex flex-col gap-4">
      <div>
        <CardTitle>{title}</CardTitle>
        {subtitle && <CardSubtitle>{subtitle}</CardSubtitle>}
      </div>
      <FleetCluster hosts={hosts} tunnels={tunnelList} stamp={snap?.generated_at ?? "none"} users={users} />
    </Card>
  );
};
