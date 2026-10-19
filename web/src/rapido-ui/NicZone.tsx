import { FC, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { formatBytes } from "utils/formatByte";
import { formatBitrate, isNumber, nicSpeedBps } from "utils/tunnelMetrics";
import { Badge } from "./Badge";
import { ClusterZone } from "./Cluster";
import { DualRing, RX_COLOR, TX_COLOR } from "./DualRing";
import { TYPE, saturatingZones, solidZones } from "./instrument";
import { TrendBars } from "./Segments";
import { useAutoMax } from "./useAutoMax";
import { useRollingSeries } from "./useRollingSeries";

// One network card as a cluster zone: a dual ring (outer = download, inner =
// upload) on one shared scale, the link speed or auto-scale note, and the two
// streams under it with their live rate, total and trend. Used by the tunnel
// card (both ends) and by the node cards on Monitoring and Nodes.

export const NicChip: FC<{ nic?: string | null; speedMbps?: number | null }> = ({ nic, speedMbps }) => {
  const speed = nicSpeedBps(speedMbps);
  if (!nic && speed === null) return null;
  return (
    <Badge tone="gray" className="px-2 py-0.5 text-[11px] font-normal" dir="ltr">
      {nic}
      {nic && speed !== null ? " · " : ""}
      {speed !== null ? `${formatBitrate(speed).value} ${formatBitrate(speed).unit}` : ""}
    </Badge>
  );
};

/** One direction under a dual ring: its colour key, the live rate, the total so far and a trend. */
export const StreamFigure: FC<{
  arrow: string;
  label: string;
  color: string;
  bps: number | null | undefined;
  totalBytes?: number | null;
  series: (number | null)[];
  max: number;
  saturating: boolean;
}> = ({ arrow, label, color, bps, totalBytes, series, max, saturating }) => {
  const { t } = useTranslation();
  const known = isNumber(bps);
  const rate = known ? formatBitrate(bps as number) : null;
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1.5">
      <div className="flex items-center gap-1.5">
        <span aria-hidden="true" className="h-3 w-1 rounded-full" style={{ background: color, boxShadow: `0 0 6px ${color}` }} />
        <span className={TYPE.caption}>
          {arrow} {label}
        </span>
      </div>
      <div className="flex items-baseline gap-1" dir="ltr">
        <span className={`${TYPE.readout} text-lg`}>{rate ? rate.value : "—"}</span>
        {rate && <span className={`${TYPE.unit} text-[11px]`}>{rate.unit}</span>}
      </div>
      <TrendBars values={series} max={max} zones={saturating ? saturatingZones(color) : solidZones(color)} height={22} bars={26} ariaLabel={`${label} trend`} />
      {isNumber(totalBytes) && (
        <div className={TYPE.caption} dir="ltr">
          {String(t("rapido.tunnels.totalLabel"))} <span className="font-medium text-rapido-text">{formatBytes(totalBytes)}</span>
        </div>
      )}
    </div>
  );
};

/** One network card: a dual ring, the link speed (or auto-scale) note, and the two streams under it. */
export const NicZone: FC<{
  title: string;
  nic?: string | null;
  speedMbps?: number | null;
  rx: number | null | undefined;
  tx: number | null | undefined;
  rxBytes?: number | null;
  txBytes?: number | null;
  seriesKey: string;
  stamp: unknown;
  ariaLabel: string;
  error?: string;
  /** A real history to draw instead of the one this page has collected itself. */
  series?: { rx: (number | null)[]; tx: (number | null)[] };
  /** Extra content under the streams (a note, a link). */
  footer?: ReactNode;
  /** An action in the zone header, next to the network card chip. */
  action?: ReactNode;
}> = ({ title, nic, speedMbps, rx, tx, rxBytes, txBytes, seriesKey, stamp, ariaLabel, error, series, footer, action }) => {
  const { t } = useTranslation();
  const L = (k: string) => String(t(k));
  const speed = nicSpeedBps(speedMbps);
  const top = Math.max(isNumber(rx) ? rx : 0, isNumber(tx) ? tx : 0);
  const auto = useAutoMax(top, 1e6);
  const max = speed ?? auto.max;
  const rxRolling = useRollingSeries(`${seriesKey}:rx`, isNumber(rx) ? rx : null, stamp);
  const txRolling = useRollingSeries(`${seriesKey}:tx`, isNumber(tx) ? tx : null, stamp);
  const rxSeries = series?.rx && series.rx.length > 1 ? series.rx : rxRolling;
  const txSeries = series?.tx && series.tx.length > 1 ? series.tx : txRolling;

  return (
    <ClusterZone
      title={title}
      chips={
        <>
          <NicChip nic={nic} speedMbps={speedMbps} />
          {action && <span className="ms-auto">{action}</span>}
        </>
      }
    >
      {error && (
        <div className="rounded-lg border border-rapido-warn/30 bg-rapido-warn/10 px-3 py-2 text-xs text-rapido-warn" dir="ltr">
          {error}
        </div>
      )}
      <div className="flex flex-col items-center gap-1">
        <DualRing
          rx={rx}
          tx={tx}
          max={max}
          saturating={speed !== null}
          size={184}
          ariaLabel={`${ariaLabel}: ↓ ${isNumber(rx) ? `${formatBitrate(rx).value} ${formatBitrate(rx).unit}` : L("rapido.gauge.unknown")}, ↑ ${isNumber(tx) ? `${formatBitrate(tx).value} ${formatBitrate(tx).unit}` : L("rapido.gauge.unknown")}`}
          rxLabel={L("rapido.tunnels.download")}
          txLabel={L("rapido.tunnels.upload")}
        />
        <div className={TYPE.caption}>
          {speed !== null
            ? String(t("rapido.tunnels.ofLink", { speed: `${formatBitrate(speed).value} ${formatBitrate(speed).unit}` }))
            : L("rapido.tunnels.autoScale") + ` · ${formatBitrate(max).value} ${formatBitrate(max).unit}`}
        </div>
      </div>
      <div className="flex gap-4 border-t border-rapido-border/60 pt-3">
        <StreamFigure arrow="↓" label={L("rapido.tunnels.download")} color={RX_COLOR} bps={rx} totalBytes={rxBytes} series={rxSeries} max={max} saturating={speed !== null} />
        <StreamFigure arrow="↑" label={L("rapido.tunnels.upload")} color={TX_COLOR} bps={tx} totalBytes={txBytes} series={txSeries} max={max} saturating={speed !== null} />
      </div>
      {footer}
    </ClusterZone>
  );
};
