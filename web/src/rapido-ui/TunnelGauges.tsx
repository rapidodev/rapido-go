import { FC } from "react";
import { useTranslation } from "react-i18next";
import { Tunnel, TunnelMetrics, TunnelNodeMetrics } from "types/Tunnel";
import { lossTotal } from "utils/tunnelMetrics";
import { Cluster, ClusterZone } from "./Cluster";
import { useNodesQuery } from "hooks/useNodesQuery";
import { TunnelNodeSlot } from "./ExtensionSlots";
import { RestartNodeButton } from "./RestartNodeModal";
import { NicZone } from "./NicZone";
import { GAUGE_CYAN, solidZones } from "./instrument";
import { ResourceMeter } from "./ResourceMeter";
import { StatusPill } from "./StatusPill";
import { useAutoMax } from "./useAutoMax";
import { useRollingSeries } from "./useRollingSeries";

// The tunnel card's instrument cluster: one plate, three zones - the node's
// network card, the relay's network card (each a dual ring: outer = download,
// inner = upload) and the relay's resources (CPU, RAM, disk, connections on one
// grid). Every reading carries a trend built from the polls this page has seen.

/** The relay's resources: four rings of one size on one grid, each with its trend. */
const ResourceZone: FC<{ tunnel: Tunnel; metrics: TunnelMetrics; stamp: unknown }> = ({ tunnel, metrics: m, stamp }) => {
  const { t } = useTranslation();
  const L = (k: string) => String(t(k));
  const memPct = m.mem_total_mb > 0 ? (m.mem_used_mb / m.mem_total_mb) * 100 : null;
  const diskPct = m.disk_total_gb > 0 ? (m.disk_used_gb / m.disk_total_gb) * 100 : null;
  const conn = useAutoMax(m.connections, 100);
  const loss = lossTotal(m);
  const key = `${tunnel.id}`;
  const cpuS = useRollingSeries(`${key}:cpu`, m.cpu_percent, stamp);
  const memS = useRollingSeries(`${key}:mem`, memPct, stamp);
  const diskS = useRollingSeries(`${key}:disk`, diskPct, stamp);
  const connS = useRollingSeries(`${key}:conn`, m.connections, stamp);
  const lossTone = loss === 0 ? "green" : loss <= 30 ? "yellow" : "red";
  const size = 100;

  return (
    <ClusterZone
      title={L("rapido.tunnels.resources")}
      chips={
        <StatusPill
          tone={lossTone}
          title={String(
            t("rapido.tunnels.metricsLossBreakdown", { rxDrop: m.rx_dropped, txDrop: m.tx_dropped, rxErr: m.rx_errors, txErr: m.tx_errors })
          )}
          className="px-2 py-0.5 text-[11px]"
          data-testid="loss-pill"
        >
          {L("rapido.tunnels.metricsLoss")} <span dir="ltr">{loss}</span>
        </StatusPill>
      }
    >
      <div className="grid grid-cols-2 justify-items-center gap-x-3 gap-y-4">
        <ResourceMeter value={m.cpu_percent} max={100} unit="%" size={size} label={L("rapido.tunnels.metricsCpu")} trend={cpuS} trendMax={100} />
        <ResourceMeter value={memPct} max={100} unit="%" size={size} label={L("rapido.tunnels.metricsRam")} sublabel={`${m.mem_used_mb} / ${m.mem_total_mb} MB`} trend={memS} trendMax={100} />
        <ResourceMeter value={diskPct} max={100} unit="%" size={size} label={L("rapido.tunnels.metricsDisk")} sublabel={`${m.disk_used_gb} / ${m.disk_total_gb} GB`} trend={diskS} trendMax={100} />
        <ResourceMeter value={m.connections} max={conn.max} size={size} label={L("rapido.tunnels.metricsConnections")} zones={solidZones(GAUGE_CYAN)} peak={conn.peak > 0 ? conn.peak : null} trend={connS} trendMax={conn.max} />
      </div>
    </ClusterZone>
  );
};

/** The whole cluster for a tunnel: node NIC, relay NIC, relay resources. */
export const TunnelCluster: FC<{ tunnel: Tunnel; metrics: TunnelMetrics; nodeName: string }> = ({
  tunnel,
  metrics: m,
  nodeName,
}) => {
  const { t } = useTranslation();
  const L = (k: string) => String(t(k));
  const stamp = m.checked_at;
  const { data: nodes } = useNodesQuery();
  const nodeRecord = nodes?.find((n) => n.id === tunnel.node_id);
  const node: TunnelNodeMetrics | undefined = m.node;
  return (
    <Cluster aria-label={L("rapido.tunnels.clusterLabel")}>
      {node && (
        <NicZone
          title={`${L("rapido.tunnels.nodeEnd")} · ${nodeName}`}
          nic={node.nic ?? tunnel.node_nic}
          speedMbps={node.nic_speed_mbps}
          rx={node.rx_bps}
          tx={node.tx_bps}
          rxBytes={node.rx_bytes}
          txBytes={node.tx_bytes}
          seriesKey={`${tunnel.id}:node`}
          stamp={stamp}
          ariaLabel={`${L("rapido.tunnels.nodeEnd")} ${nodeName}`}
          error={node.error}
          footer={
            <TunnelNodeSlot tunnelId={tunnel.id} nodeId={tunnel.node_id} nodeName={nodeName} />
          }
          action={<RestartNodeButton node={{ id: tunnel.node_id, name: nodeName, ssh_known: nodeRecord?.ssh_known }} />}
        />
      )}
      <NicZone
        title={L("rapido.tunnels.relayEnd")}
        nic={m.nic ?? tunnel.relay_nic}
        speedMbps={m.nic_speed_mbps}
        rx={m.nic_rx_bps}
        tx={m.nic_tx_bps}
        rxBytes={m.nic_rx_bytes ?? m.rx_bytes}
        txBytes={m.nic_tx_bytes ?? m.tx_bytes}
        seriesKey={`${tunnel.id}:relay`}
        stamp={stamp}
        ariaLabel={L("rapido.tunnels.relayEnd")}
      />
      <ResourceZone tunnel={tunnel} metrics={m} stamp={stamp} />
    </Cluster>
  );
};
