import { FC, useMemo } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { useIsSudo } from "hooks/useCurrentAdminQuery";
import { useSystemStatsQuery } from "hooks/useSystemStatsQuery";
import { useSystemUsageHistoryQuery } from "hooks/useSystemUsageHistoryQuery";
import { formatBytes, numberWithCommas } from "utils/formatByte";
import { formatPercent } from "utils/formatPercent";
import { PulseDot } from "rapido-ui/Badge";
import { Card, CardSubtitle, CardTitle } from "rapido-ui/Card";
import { Cluster, ClusterZone } from "rapido-ui/Cluster";
import { DistributionRing } from "rapido-ui/DistributionRing";
import { FleetOverview } from "rapido-ui/FleetBoard";
import { GAUGE_CYAN, GAUGE_GOOD, GAUGE_VIOLET, TYPE, solidZones } from "rapido-ui/instrument";
import { ResourceMeter } from "rapido-ui/ResourceMeter";
import { SegmentBar, TrendBars } from "rapido-ui/Segments";
import { TimeSeriesChart } from "rapido-ui/TimeSeriesChart";
import { useChartPalette } from "rapido-ui/appearance";
import { useRollingSeries } from "rapido-ui/useRollingSeries";
import "rapido-ui/tailwind.css";

// The Overview is written in the same instrument language as the tunnel and
// node cards. A "Users" cluster (active against total, who is online now, how
// users split by status, what they have used), the usage chart, and - for a
// sudo admin - the same fleet power view the Monitoring page opens with: one
// board for every node and tunnel. Every number is read from /system,
// /system/usage-history and /monitoring; anything with no data is left out.

type StatusRow = { status: string; label: string; value: number };

// The history's points are calendar days, placed at midnight UTC. Read in the
// browser's own zone they would land on the day before anywhere west of Greenwich.
export const usageDayLabel = (ms: number, locale: string): string =>
  new Date(ms).toLocaleDateString(locale, { month: "2-digit", day: "2-digit", timeZone: "UTC" });

// The legend names each status next to its ring, with its share as a segmented
// bar. Every status is listed - a zero row is dimmed rather than dropped - so
// the list keeps its shape as the counts change on each poll.
const StatusLegend: FC<{ rows: StatusRow[]; colors: Record<string, string>; label: string }> = ({ rows, colors, label }) => {
  const total = rows.reduce((sum, r) => sum + r.value, 0);
  return (
    <ul aria-label={label} className="flex min-w-[12rem] max-w-sm flex-1 flex-col gap-2.5">
      {rows.map((r) => (
        <li key={r.status} className={classNames("flex flex-col gap-1", r.value === 0 && "opacity-50")}>
          <div className="flex items-center gap-2.5 text-sm">
            <span aria-hidden="true" className="h-2.5 w-1.5 shrink-0 rounded-full" style={{ backgroundColor: colors[r.status] }} />
            <span className="min-w-0 flex-1 truncate text-rapido-text">{r.label}</span>
            <span className={classNames(TYPE.readout, "text-sm")} dir="ltr">
              {numberWithCommas(r.value) ?? "0"}
            </span>
            <span className="w-12 text-end text-xs tabular-nums text-rapido-muted" dir="ltr">
              {formatPercent(r.value, total)}
            </span>
          </div>
          <SegmentBar value={r.value} max={Math.max(1, total)} count={24} zones={[{ upTo: 1, color: colors[r.status] }]} className="h-1.5" />
        </li>
      ))}
    </ul>
  );
};

const OverviewContent: FC = () => {
  const { t, i18n } = useTranslation();
  const pal = useChartPalette();

  const isSudo = useIsSudo();
  const { data: stats, isLoading: statsLoading, dataUpdatedAt } = useSystemStatsQuery();
  // Panel-wide traffic, which is not a reseller's business: the server answers
  // them with zeros, so they do not ask, and the panels below stay hidden.
  const { data: history, isLoading: historyLoading } = useSystemUsageHistoryQuery(14, isSudo);
  const onlineTrend = useRollingSeries("overview:online", stats?.online_users, dataUpdatedAt);
  const activeTrend = useRollingSeries("overview:active", stats?.users_active, dataUpdatedAt);

  // `status` keys the colour map, `label` is the legend text.
  const statusRows: StatusRow[] = stats
    ? [
        { status: "active", label: t("status.active"), value: stats.users_active },
        { status: "on_hold", label: t("status.on_hold"), value: stats.users_on_hold },
        { status: "limited", label: t("status.limited"), value: stats.users_limited },
        { status: "expired", label: t("status.expired"), value: stats.users_expired },
        { status: "disabled", label: t("status.disabled"), value: stats.users_disabled },
      ]
    : [];
  // The status colours are the panel's trio plus violet; "disabled" is the quiet one.
  const statusColors: Record<string, string> = {
    active: pal.good,
    on_hold: pal.warn,
    limited: pal.warm,
    expired: pal.violet,
    disabled: pal.muted,
  };

  // incoming_bandwidth/outgoing_bandwidth are always 0 today - honestly, per
  // internal/httpapi/system.go's own comment, because there is no
  // usage-reporting pipeline from nodes yet. When they are zero the figure
  // shown is what the daily history really recorded.
  const lifetime = stats ? stats.incoming_bandwidth + stats.outgoing_bandwidth : 0;
  const days = history ?? [];
  const periodTotal = days.reduce((a, p) => a + (p.usage || 0), 0);
  const hasUsage = isSudo && (lifetime > 0 || periodTotal > 0);

  const usageSeries = useMemo(
    () =>
      history
        ? [
            {
              key: "usage",
              label: String(t("userDialog.usage")),
              color: GAUGE_CYAN,
              points: history.map((p) => ({ t: Date.parse(`${p.date}T00:00:00Z`), v: p.usage })),
            },
          ]
        : [],
    [history, t]
  );

  const total = Math.max(1, stats?.total_user ?? 1, stats?.online_users ?? 0);

  return (
    <div className="p-4 sm:p-6">
      <h1 className="mb-1 text-2xl font-bold">{t("rapido.overview")}</h1>
      <p className="mb-6 text-sm text-rapido-muted">{t("rapido.overviewSubtitle")}</p>

      <div className="flex flex-col gap-4">
        {/* The hero: who is online now, how the users split by status, what they used - one panel, no holes */}
        <Card tilt tiltMax={1} className="p-3 sm:p-4">
          <Cluster
            fixed
            className={classNames(
              "grid-cols-1 md:grid-cols-2",
              hasUsage ? "lg:grid-cols-[minmax(15rem,1fr)_minmax(21rem,2fr)_minmax(15rem,1fr)]" : "lg:grid-cols-[minmax(15rem,1fr)_minmax(21rem,2fr)]"
            )}
            aria-label={String(t("rapido.users.clusterLabel"))}
          >
            <ClusterZone
              title={t("rapido.onlineNow")}
              chips={
                <span className="flex items-center gap-1.5 text-[11px] text-rapido-muted">
                  <PulseDot tone="brand" live />
                  {t("rapido.live")}
                </span>
              }
            >
              <div className="flex flex-1 flex-col items-center justify-center gap-3">
                <ResourceMeter
                  value={statsLoading ? null : stats?.online_users ?? 0}
                  max={total}
                  size={150}
                  loading={statsLoading}
                  zones={solidZones(GAUGE_CYAN)}
                  ariaLabel={String(t("rapido.onlineNow"))}
                  sublabel={t("rapido.onlineNowDesc")}
                />
                <TrendBars
                  values={onlineTrend}
                  bars={36}
                  height={30}
                  max={Math.max(1, ...onlineTrend.filter((v): v is number => typeof v === "number"))}
                  zones={solidZones(GAUGE_CYAN)}
                  className="max-w-[16rem]"
                  ariaLabel={String(t("rapido.liveTrend"))}
                />
              </div>
            </ClusterZone>

            <ClusterZone title={t("rapido.userStatusTitle")} className="md:order-3 md:col-span-2 lg:order-none lg:col-span-1">
              {/* The legend sits beside the ring and drops under it when the zone is
                  too narrow for both (flex-wrap; the ring has a fixed size, the legend
                  takes the rest of the row). */}
              <div className="flex flex-1 flex-wrap items-center justify-center gap-x-7 gap-y-5">
                <DistributionRing
                  size={156}
                  ariaLabel={String(t("rapido.userStatusTitle"))}
                  slices={statusRows.map((r) => ({ key: r.status, value: r.value, color: statusColors[r.status] }))}
                  caption={t("rapido.totalUsers")}
                />
                {!statsLoading && stats && (
                  <StatusLegend rows={statusRows} colors={statusColors} label={String(t("rapido.userStatusLegend"))} />
                )}
              </div>
            </ClusterZone>

            {hasUsage && (
              <ClusterZone title={t("rapido.users.trafficUsed")}>
                <div className="flex flex-1 flex-col justify-between gap-4">
                  <div className="flex flex-col gap-1">
                    <span className={classNames(TYPE.readout, "text-4xl")} dir="ltr">
                      {String(formatBytes(lifetime > 0 ? lifetime : periodTotal))}
                    </span>
                    <span className={TYPE.caption}>
                      {lifetime > 0 ? t("dataUsage") : t("rapido.users.lastDays", { days: days.length })}
                    </span>
                  </div>
                  {days.length > 1 && (
                    <TrendBars values={days.map((d) => d.usage)} bars={days.length} height={64} zones={solidZones(GAUGE_VIOLET)} ariaLabel={String(t("rapido.usageOverTimeTitle"))} />
                  )}
                </div>
              </ClusterZone>
            )}
          </Cluster>
        </Card>

        {/* The fleet power view: the same board the Monitoring page opens with */}
        {isSudo && <FleetOverview users={stats ? { online: stats.online_users, total: stats.total_user } : undefined} />}

        {/* The usage over time, with a hover / keyboard readout */}
        {isSudo && (
          <Card className="flex min-w-0 flex-col gap-3">
            <div>
              <CardTitle>{t("rapido.usageOverTimeTitle")}</CardTitle>
              <CardSubtitle>{t("rapido.usageOverTimeDesc")}</CardSubtitle>
            </div>
            <TimeSeriesChart
              series={historyLoading ? [] : usageSeries}
              format={(v) => String(formatBytes(v, 1))}
              formatTime={(ms) => usageDayLabel(ms, i18n.language)}
              ariaLabel={String(t("rapido.usageOverTimeTitle"))}
              height={230}
              empty={String(t("rapido.monitoring.noHistory"))}
            />
          </Card>
        )}
      </div>
    </div>
  );
};

export const OverviewNew: FC = () => <OverviewContent />;

export default OverviewNew;
