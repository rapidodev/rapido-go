import { FC, ReactNode } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { NodeLoadEntry } from "types/HostLoad";
import { MonitoringHistoryPoint, MonitoringHost } from "types/Monitoring";
import { Tunnel } from "types/Tunnel";
import { ipFamily, isNumber } from "utils/tunnelMetrics";
import { handshakeAge, sortTunnels, tunnelStatus } from "utils/tunnelStatus";
import { formatBytes } from "utils/formatByte";
import { activeServerOf } from "utils/tunnelMetrics";
import { Cluster, ClusterZone } from "./Cluster";
import { NodeLinksSlot } from "./ExtensionSlots";
import { GAUGE_CYAN, LOAD_ZONES, TYPE, UNLIT, glowShadow, solidZones, GAUGE_BAD, GAUGE_GOOD, GAUGE_WARN } from "./instrument";
import { NicZone } from "./NicZone";
import { ResourceMeter } from "./ResourceMeter";
import { useAutoMax } from "./useAutoMax";
import { useRollingSeries } from "./useRollingSeries";

// A node's instrument cluster - the same plate, zones, rings and type as the
// tunnel card: its network card as a dual ring, its resources on one grid, and
// a strip of indicator lights for what it is linked to. Every zone is built
// from numbers the payloads really carry; a zone with nothing to show is left
// out rather than filled with zeros.

export type LightState = "up" | "degraded" | "down" | "unknown";

const lightColor: Record<LightState, string> = {
  up: GAUGE_GOOD,
  degraded: GAUGE_WARN,
  down: GAUGE_BAD,
  unknown: UNLIT,
};

/** One indicator light: a lit LED in the status colour, the name, and a word. */
export const Light: FC<{
  label: string;
  state: LightState;
  stateLabel: string;
  detail?: ReactNode;
  /** Extra caption lines under the state (handshake age, traffic, an error). */
  lines?: ReactNode[];
  title?: string;
}> = ({ label, state, stateLabel, detail, lines, title }) => {
  const color = lightColor[state];
  return (
    <li
      title={title}
      data-state={state}
      className={classNames(
        "flex min-w-0 items-center gap-2.5 rounded-lg border px-3 py-2",
        state === "down" ? "border-rapido-bad/50 bg-rapido-bad/10" : "border-rapido-border/70 bg-rapido-raised/30"
      )}
    >
      <span
        aria-hidden="true"
        className="h-2.5 w-2.5 shrink-0 rounded-full"
        style={{ background: color, boxShadow: state === "unknown" ? undefined : glowShadow(color) }}
      />
      <span className="flex min-w-0 flex-col">
        <span className="truncate text-xs font-medium text-rapido-text" dir="ltr">
          {label}
        </span>
        <span className={classNames(TYPE.caption, state === "down" && "text-rapido-bad")}>
          {stateLabel}
          {detail ? <> · {detail}</> : null}
        </span>
        {lines?.map((l, i) => (
          <span key={i} className={classNames(TYPE.caption, "break-words")}>
            {l}
          </span>
        ))}
      </span>
    </li>
  );
};

const agoKey = {
  seconds: "rapido.monitoring.agoSeconds",
  minutes: "rapido.monitoring.agoMinutes",
  hours: "rapido.monitoring.agoHours",
  days: "rapido.monitoring.agoDays",
} as const;

const bps = (bytesPerSecond: number | null | undefined) => (isNumber(bytesPerSecond) ? bytesPerSecond * 8 : null);

const seriesOf = (points: MonitoringHistoryPoint[] | undefined, pick: (p: MonitoringHistoryPoint) => number | null) =>
  points && points.length > 1 ? points.map(pick) : undefined;

export const NodeCluster: FC<{
  host: MonitoringHost;
  load?: NodeLoadEntry;
  history?: MonitoringHistoryPoint[];
  /** The tunnels this node is the node end of. */
  tunnels?: Tunnel[];
  className?: string;
}> = ({ host, load, history, tunnels = [], className }) => {
  const { t } = useTranslation();
  const L = (k: string) => String(t(k));
  const id = host.node_id ?? "panel";
  const stamp = host.collected_at ?? "none";

  const rxHist = seriesOf(history, (p) => bps(p.rx));
  const txHist = seriesOf(history, (p) => bps(p.tx));
  const cpuRolling = useRollingSeries(`node:${id}:cpu`, host.cpu_percent, stamp);
  const memRolling = useRollingSeries(`node:${id}:mem`, host.mem_percent, stamp);
  const diskRolling = useRollingSeries(`node:${id}:disk`, host.disk_percent, stamp);
  const connRolling = useRollingSeries(`node:${id}:conn`, host.connections, stamp);
  const cpuS = seriesOf(history, (p) => p.cpu) ?? cpuRolling;
  const memS = seriesOf(history, (p) => p.mem) ?? memRolling;
  const connS = seriesOf(history, (p) => p.conns) ?? connRolling;
  const conn = useAutoMax(host.connections, 100);

  const hasNic = isNumber(host.rx_rate) || isNumber(host.tx_rate);
  const links = host.tunnels ?? [];
  const hasLinks = links.length > 0 || tunnels.length > 0;

  const connMax = load ? load.capacity : conn.max;
  const connZones = load ? LOAD_ZONES : solidZones(GAUGE_CYAN);

  return (
    <Cluster className={className} aria-label={L("rapido.monitoring.clusterLabel")}>
      {hasNic && (
        <NicZone
          title={L("rapido.monitoring.network")}
          rx={bps(host.rx_rate)}
          tx={bps(host.tx_rate)}
          seriesKey={`node:${id}:nic`}
          stamp={stamp}
          ariaLabel={`${host.name} ${L("rapido.monitoring.network")}`}
          series={rxHist && txHist ? { rx: rxHist, tx: txHist } : undefined}
        />
      )}

      <ClusterZone title={L("rapido.tunnels.resources")}>
        <div className="grid grid-cols-2 justify-items-center gap-x-3 gap-y-4">
          <ResourceMeter value={host.cpu_percent} max={100} unit="%" size={100} label={L("rapido.monitoring.cpu")} trend={cpuS} trendMax={100} />
          <ResourceMeter value={host.mem_percent} max={100} unit="%" size={100} label={L("rapido.monitoring.memory")} trend={memS} trendMax={100} />
          <ResourceMeter value={host.disk_percent} max={100} unit="%" size={100} label={L("rapido.monitoring.disk")} trend={diskRolling} trendMax={100} />
          <ResourceMeter
            value={host.connections}
            max={connMax}
            size={100}
            label={L("rapido.monitoring.connections")}
            sublabel={load ? `${load.conns} / ${load.capacity}` : undefined}
            zones={connZones}
            trend={connS}
            trendMax={connMax}
          />
        </div>
      </ClusterZone>

      {hasLinks && (
        <ClusterZone title={L("rapido.monitoring.links")}>
          <ul className="flex flex-col gap-2">
            {tunnels.map((tn) => {
              const up = tn.health ? tn.health.up : null;
              const hap = tn.metrics?.node?.haproxy;
              const fam = ipFamily(hap?.present ? activeServerOf(hap)?.addr : tn.relay_tunnel_addr || tn.relay_host);
              const state: LightState = up === null ? "unknown" : up ? "up" : "down";
              return (
                <Light
                  key={`t${tn.id}`}
                  label={tn.name}
                  state={state}
                  stateLabel={state === "up" ? L("rapido.monitoring.tunnelUp") : state === "down" ? L("rapido.monitoring.tunnelDown") : L("rapido.tunnels.healthUnknown")}
                  detail={fam ? `${fam} · ${L("rapido.tunnels.haproxyActive")}` : undefined}
                  title={tn.health?.error || undefined}
                />
              );
            })}
            {sortTunnels(links).map((ex) => {
              const st = tunnelStatus(ex);
              const age = handshakeAge(ex);
              const ageText = age.kind === "never" ? L("rapido.monitoring.tunnelNever") : String(t(agoKey[age.unit], { value: age.value }));
              const state: LightState = st === "up" ? "up" : st === "down-fallback" ? "degraded" : "down";
              const label =
                st === "up"
                  ? L("rapido.monitoring.tunnelUp")
                  : st === "down-fallback"
                  ? L("rapido.monitoring.tunnelDownFallback")
                  : st === "missing"
                  ? L("rapido.monitoring.tunnelMissing")
                  : L("rapido.monitoring.tunnelDown");
              return (
                <Light
                  key={`x${ex.name}`}
                  label={ex.name}
                  state={state}
                  stateLabel={label}
                  detail={ex.present && ex.probe_ms != null ? `${Math.round(ex.probe_ms)} ms` : undefined}
                  title={ex.error || undefined}
                  lines={[
                    ...(ex.present
                      ? [
                          `${L("rapido.monitoring.lastHandshake")}: ${ageText}`,
                          <span key="bytes" className="tabular-nums" dir="ltr">
                            ↓ {formatBytes(ex.rx_bytes)} · ↑ {formatBytes(ex.tx_bytes)}
                          </span>,
                        ]
                      : []),
                    ...(st === "missing" && ex.fallback_active ? [L("rapido.monitoring.tunnelFallbackChip")] : []),
                    ...(ex.error ? [<span key="err">{L("rapido.monitoring.tunnelError")}: <span dir="ltr">{ex.error}</span></span>] : []),
                  ]}
                />
              );
            })}
          </ul>
          <NodeLinksSlot
            nodeId={host.node_id ?? null}
            tunnelId={tunnels[0]?.id ?? null}
            links={{ total: links.length, down: links.filter((ex) => tunnelStatus(ex) !== "up").length }}
          />
        </ClusterZone>
      )}
    </Cluster>
  );
};
