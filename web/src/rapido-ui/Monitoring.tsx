import { FC, useMemo, useState } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";

import { useMonitoringHistoryQuery, useMonitoringQuery } from "hooks/useMonitoringQuery";
import { useNodesQuery } from "hooks/useNodesQuery";
import { useSystemStatsQuery } from "hooks/useSystemStatsQuery";
import { useTunnelsQuery } from "hooks/useTunnelsQuery";
import { MonitoringHost } from "types/Monitoring";
import { agoText } from "utils/ago";
import { formatDuration } from "utils/tunnelMetrics";
import { hostDisplayState, hostTone } from "utils/monitoringHost";
import { FleetTunnelSummary, summarizeFleetTunnels, tunnelProblemLabel } from "utils/tunnelStatus";
import { AreaChart } from "./AreaChart";
import { Badge, PulseDot } from "./Badge";
import { Button } from "./Button";
import { Card, CardSubtitle, CardTitle } from "./Card";
import { Cluster, ClusterZone } from "./Cluster";
import { FleetOverview } from "./FleetBoard";
import { GAUGE_CYAN, GAUGE_GOOD, GAUGE_VIOLET, TYPE } from "./instrument";
import { NodeCluster } from "./NodeCluster";
import { PortList } from "./PortChips";
import { RestartNodeButton } from "./RestartNodeModal";
import { formatBitrateText } from "utils/tunnelMetrics";

// The Monitoring page: a fleet overview on top (one board for every node and
// tunnel), then one card per node in the same instrument-cluster template the
// tunnel cards use. Built from what GET /monitoring really reports
// (internal/httpapi/monitoring.go's monitoringHostDTO); the Go node agent does
// not report per-request counts, so none are shown. WireGuard links are
// reported per tunnel (status, handshake age, probe latency, traffic, whether
// the direct fallback is carrying it), with the classification and sorting in
// utils/tunnelStatus.ts.

const TUNNEL_WARNING_LIMIT = 6;

export const TunnelWarning: FC<{ summary: FleetTunnelSummary }> = ({ summary }) => {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  if (summary.severity === "none") return null;

  const { problems, withFallback } = summary;
  const withoutFallback = problems.length - withFallback;
  const shown = expanded ? problems : problems.slice(0, TUNNEL_WARNING_LIMIT);

  return (
    <div
      role="alert"
      className={classNames(
        "rounded-xl border px-4 py-3 text-sm",
        summary.severity === "critical"
          ? "border-red-500/30 bg-red-500/10 text-red-300"
          : "border-amber-500/40 bg-amber-500/10 text-amber-300"
      )}
    >
      <div className="font-semibold">
        {t("rapido.monitoring.tunnelsNotUpTitle", { down: problems.length, total: summary.total })}
      </div>
      <ul className="mt-2 flex flex-wrap gap-1.5">
        {shown.map((p, i) => (
          <li key={`${i}-${tunnelProblemLabel(p)}`} className="rounded-full bg-black/25 px-2.5 py-0.5 text-xs" dir="ltr">
            {tunnelProblemLabel(p)}
          </li>
        ))}
      </ul>
      {problems.length > TUNNEL_WARNING_LIMIT && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-2 text-xs underline underline-offset-2 hover:no-underline"
        >
          {expanded
            ? t("rapido.monitoring.tunnelsShowFewer")
            : t("rapido.monitoring.tunnelsShowAll", { n: problems.length })}
        </button>
      )}
      {withFallback > 0 && (
        <p className="mt-2 text-xs">{t("rapido.monitoring.tunnelsFallbackNote", { n: withFallback })}</p>
      )}
      {withoutFallback > 0 && (
        <p className="mt-1 text-xs">{t("rapido.monitoring.tunnelsNoFallbackNote", { n: withoutFallback })}</p>
      )}
    </div>
  );
};

const HostCard: FC<{ host: MonitoringHost }> = ({ host }) => {
  const { t } = useTranslation();
  const L = (k: string, o?: Record<string, unknown>) => String(o ? t(k, o) : t(k));
  const [open, setOpen] = useState(false);
  // The trends in the cluster read this too, so it is always fetched (one request
  // per node, shared with the fleet chart through the same query key).
  const { data: history, isLoading: historyLoading } = useMonitoringHistoryQuery(host.node_id, true);
  const { data: nodes } = useNodesQuery();
  const { data: tunnels } = useTunnelsQuery();

  const node = host.node_id === null ? undefined : nodes?.find((n) => n.id === host.node_id);
  const nodeTunnels = host.node_id === null ? [] : (tunnels ?? []).filter((tn) => tn.node_id === host.node_id);

  const state = hostDisplayState(host);
  const tone = hostTone(host);
  const statusLabel =
    state === "healthy"
      ? L("rapido.monitoring.online")
      : state === "stale"
      ? L("rapido.monitoring.stale")
      : state === "unhealthy"
      ? L("rapido.monitoring.unhealthy")
      : L("rapido.monitoring.unreachable");

  const borderTone =
    state === "healthy"
      ? "border-emerald-500/30"
      : state === "stale"
      ? "border-yellow-500/30"
      : state === "unhealthy"
      ? "border-red-500/30"
      : "border-rapido-border";

  const hasTunnelCount = (host.tunnels ?? []).length === 0 && (host.tunnels_total ?? 0) > 0;

  return (
    <Card tilt tiltMax={1.5} className={classNames("flex flex-col gap-4 p-4 sm:p-5", borderTone)}>
      {/* header bar: identity and health on the left, actions at the end */}
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div className="flex min-w-0 flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <PulseDot tone={tone} live={state === "healthy"} />
            <CardTitle className="flex flex-wrap items-center gap-2 text-base">
              <span className="truncate">{host.name}</span>
              <Badge tone={tone}>{statusLabel}</Badge>
              {host.node_id === null && <Badge tone="brand">{L("rapido.monitoring.panel")}</Badge>}
            </CardTitle>
          </div>
          <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-rapido-muted">
            <span className="rounded-md bg-rapido-raised px-1.5 py-0.5 text-rapido-text" dir="ltr">
              {host.address || L("rapido.monitoring.thisMachine")}
            </span>
            {host.xray_version && (
              <span className="rounded-md bg-rapido-raised px-1.5 py-0.5" dir="ltr">
                {host.xray_version}
              </span>
            )}
            {typeof host.uptime === "number" && host.uptime > 0 && (
              <span dir="ltr">{L("rapido.monitoring.uptime")} {formatDuration(host.uptime)}</span>
            )}
            {host.collected_at && (
              <span>
                {L("rapido.monitoring.lastSeen")} {agoText(t, host.collected_at)}
              </span>
            )}
            {hasTunnelCount && (
              <span dir="ltr">
                {L("rapido.monitoring.tunnels")} {host.tunnels_up ?? 0}/{host.tunnels_total}
              </span>
            )}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {state !== "no-data" && (
            <Button variant="chip" tone="accent" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
              {open ? L("rapido.monitoring.hideHistory") : L("rapido.monitoring.showHistory")}
            </Button>
          )}
          {host.node_id !== null && <RestartNodeButton node={{ id: host.node_id, name: host.name, ssh_known: node?.ssh_known }} />}
        </div>
      </div>

      {state === "no-data" ? (
        <div className="rounded-lg border border-rapido-border bg-rapido-raised px-3 py-2 text-xs text-rapido-muted">
          {L("rapido.monitoring.noAgent")}
        </div>
      ) : (
        <>
          <NodeCluster host={host} history={history} tunnels={nodeTunnels} />

          {open && (
            <Cluster minZone="16rem">
              {historyLoading || !history ? (
                <ClusterZone title={L("rapido.monitoring.historyTitle")}>
                  <div className={TYPE.caption}>{L("rapido.monitoring.loading")}</div>
                </ClusterZone>
              ) : history.length < 2 ? (
                <ClusterZone title={L("rapido.monitoring.historyTitle")}>
                  <div className={TYPE.caption}>{L("rapido.monitoring.notEnoughHistory")}</div>
                </ClusterZone>
              ) : (
                <>
                  <ClusterZone title={L("rapido.monitoring.cpu")}>
                    <AreaChart
                      values={history.map((p) => p.cpu)}
                      color={GAUGE_GOOD}
                      min={0}
                      max={100}
                      bands={[
                        { from: 70, color: "rgb(var(--rp-warn))" },
                        { from: 90, color: "rgb(var(--rp-bad))" },
                      ]}
                      formatValue={(v) => `${Math.round(v)}%`}
                      height={84}
                    />
                  </ClusterZone>
                  <ClusterZone title={`↓ ${L("rapido.monitoring.download")}`}>
                    <AreaChart values={history.map((p) => (p.rx == null ? null : p.rx * 8))} color={GAUGE_CYAN} formatValue={formatBitrateText} height={84} />
                  </ClusterZone>
                  <ClusterZone title={`↑ ${L("rapido.monitoring.upload")}`}>
                    <AreaChart values={history.map((p) => (p.tx == null ? null : p.tx * 8))} color={GAUGE_VIOLET} formatValue={formatBitrateText} height={84} />
                  </ClusterZone>
                </>
              )}
            </Cluster>
          )}

          {/* footer: the ports this node listens on, with room for an events strip */}
          {node?.listen_ports && node.listen_ports.length > 0 && (
            <div className="flex flex-wrap items-center gap-2 border-t border-rapido-border/60 pt-3">
              <span className={TYPE.caption}>
                {L("rapido.monitoring.listenPorts")} ({node.listen_ports.length})
              </span>
              <PortList ports={node.listen_ports} />
            </div>
          )}
        </>
      )}
    </Card>
  );
};

export const Monitoring: FC = () => {
  const { t } = useTranslation();
  const { data: snap, isLoading, isError } = useMonitoringQuery();
  const { data: stats } = useSystemStatsQuery();
  const hosts = useMemo(() => snap?.hosts ?? [], [snap]);
  const tunnelSummary = useMemo(() => summarizeFleetTunnels(hosts), [hosts]);

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold text-rapido-text">{t("rapido.monitoring.title")}</h1>
        <p className="text-sm text-rapido-muted">{t("rapido.monitoring.subtitle")}</p>
      </div>

      {isError && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
          {t("rapido.monitoring.failed")}
        </div>
      )}

      {/* The fleet overview - cluster, board and over-time chart in one panel - comes first. */}
      <FleetOverview users={stats ? { online: stats.online_users, total: stats.total_user } : undefined} />

      <TunnelWarning summary={tunnelSummary} />

      {isLoading ? (
        <div className="text-sm text-rapido-muted">{t("rapido.monitoring.loading")}</div>
      ) : (
        <div className="grid gap-4">
          {hosts.map((h) => (
            <HostCard key={h.node_id ?? "panel"} host={h} />
          ))}
        </div>
      )}
    </div>
  );
};

export default Monitoring;
