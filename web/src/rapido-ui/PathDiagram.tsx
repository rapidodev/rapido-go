import { FC, ReactNode } from "react";
import classNames from "classnames";
import { useTranslation } from "react-i18next";
import { ArrowsRightLeftIcon, GlobeAltIcon, ServerIcon } from "@heroicons/react/24/outline";
import { HaproxyInfo, HaproxyServer } from "types/Tunnel";
import { failoverInEffect, formatDuration, haproxyStatus, ipFamily, isActiveServer } from "utils/tunnelMetrics";
import { Badge, BadgeTone, PulseDot } from "./Badge";
import { GAUGE_CYAN, TYPE, UNLIT, solidZones } from "./instrument";
import { TrendBars } from "./Segments";
import { StatusPill } from "./StatusPill";
import { useRollingSeries } from "./useRollingSeries";

// The path diagram: NODE -> (HAProxy) -> one pipe per way to the relay -> RELAY.
// The pipe carrying traffic is lit and its dots flow; the others sit still and
// take their colour from their status. A tunnel without HAProxy has one direct
// pipe, so every tunnel reads with the same grammar. A failover (a backup pipe
// carrying the traffic) raises an amber banner.

export type DirectPath = {
  name: string;
  addr: string;
  /** null = not probed yet. */
  up: boolean | null;
  error?: string;
  downForSeconds?: number;
};

type Pipe = {
  key: string;
  name: string;
  addr: string;
  status: string;
  tone: BadgeTone;
  kind: "up" | "degraded" | "down" | "maint" | "unknown";
  backup: boolean;
  active: boolean;
  sessions?: number;
  total?: number;
  held?: number;
  detail?: string;
};

const toneText: Record<BadgeTone, string> = {
  green: "text-rapido-good",
  yellow: "text-rapido-warn",
  red: "text-rapido-bad",
  orange: "text-rapido-warn",
  sky: "text-rapido-accent",
  brand: "text-rapido-accent",
  violet: "text-rapido-violet",
  gray: "text-rapido-muted",
};

/** The pipe itself: a rounded track; lit with flowing dots when it carries traffic. */
const PipeLine: FC<{ active: boolean; kind: Pipe["kind"] }> = ({ active, kind }) => (
  <div
    aria-hidden="true"
    className={classNames(
      "relative h-2.5 w-full overflow-hidden rounded-full rtl:-scale-x-100",
      active ? "text-rapido-accent" : kind === "down" ? "text-rapido-bad" : kind === "degraded" ? "text-rapido-warn" : "text-rapido-muted"
    )}
    style={{ background: active ? "rgb(var(--rp-accent) / 0.16)" : UNLIT, boxShadow: active ? "0 0 10px rgb(var(--rp-accent) / 0.35)" : undefined }}
  >
    {active ? (
      <div className="rp-flow-dots animate-rp-flow" style={{ filter: "drop-shadow(0 0 3px rgb(var(--rp-accent) / .8))" }} />
    ) : (
      <div className="rp-flow-dots opacity-40" />
    )}
  </div>
);

const Station: FC<{ icon: ReactNode; title: string; sub?: string; lit: boolean; className?: string }> = ({
  icon,
  title,
  sub,
  lit,
  className,
}) => (
  <div
    className={classNames(
      "flex min-w-[5.5rem] max-w-[10rem] flex-col items-center gap-1 rounded-lg border bg-rapido-raised/40 px-3 py-2 text-center shadow-rp-inset",
      lit ? "border-rapido-accent/50" : "border-rapido-border",
      className
    )}
  >
    <span className={classNames("[&>svg]:h-5 [&>svg]:w-5", lit ? "text-rapido-accent" : "text-rapido-muted")} aria-hidden="true">
      {icon}
    </span>
    <span className="text-xs font-medium text-rapido-text">{title}</span>
    {sub && (
      <span className="max-w-full truncate text-[11px] text-rapido-muted" dir="ltr" title={sub}>
        {sub}
      </span>
    )}
  </div>
);

const PipeRow: FC<{ pipe: Pipe; tunnelId: number; stamp: unknown }> = ({ pipe, tunnelId, stamp }) => {
  const { t } = useTranslation();
  const sessions = useRollingSeries(`${tunnelId}:pipe:${pipe.key}`, pipe.sessions, stamp, 30);
  const fam = ipFamily(pipe.addr);
  return (
    <li
      data-active={pipe.active ? "true" : "false"}
      className={classNames(
        "flex flex-col gap-1.5 rounded-lg border px-3 py-2.5 transition-colors",
        pipe.active ? "border-rapido-accent/50 bg-rapido-accent/[0.07]" : "border-rapido-border/70 bg-rapido-raised/20"
      )}
    >
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <StatusPill tone={pipe.tone} live={pipe.kind === "up" && pipe.active} data-testid="haproxy-status">
          {pipe.status}
        </StatusPill>
        <span className="text-sm font-medium text-rapido-text" dir="ltr">
          {pipe.name}
        </span>
        {fam && (
          <Badge tone="gray" className="px-1.5 py-0.5 text-[10px] font-semibold">
            {fam}
          </Badge>
        )}
        {pipe.backup && <Badge tone="violet">{String(t("rapido.tunnels.haproxyBackup"))}</Badge>}
        {pipe.active && (
          <Badge tone="brand" data-testid="haproxy-active">
            <PulseDot tone="brand" />
            {String(t("rapido.tunnels.haproxyActive"))}
          </Badge>
        )}
        {pipe.sessions !== undefined && (
          <span className="ms-auto flex items-center gap-2">
            <span className={TYPE.caption}>
              {String(t("rapido.tunnels.haproxySessions"))}{" "}
              <span className={classNames(TYPE.readout, "text-sm")} dir="ltr">
                {pipe.sessions}
              </span>
            </span>
            <span className="w-14">
              <TrendBars values={sessions} bars={14} height={14} zones={solidZones(GAUGE_CYAN)} ariaLabel={String(t("rapido.tunnels.haproxySessions"))} />
            </span>
          </span>
        )}
      </div>
      <PipeLine active={pipe.active} kind={pipe.kind} />
      <div className={classNames(TYPE.caption, "flex flex-wrap items-center gap-x-2")}>
        <span className="tabular-nums" dir="ltr">
          {pipe.addr}
        </span>
        {pipe.held !== undefined && (
          <>
            <span aria-hidden="true">·</span>
            <span className={toneText[pipe.tone]}>
              {String(t("rapido.tunnels.haproxyHeld", { status: pipe.status, time: formatDuration(pipe.held) }))}
            </span>
          </>
        )}
        {pipe.total !== undefined && (
          <>
            <span aria-hidden="true">·</span>
            <span>
              {String(t("rapido.tunnels.haproxyTotal"))} <span dir="ltr">{pipe.total}</span>
            </span>
          </>
        )}
        {pipe.detail && (
          <>
            <span aria-hidden="true">·</span>
            <span dir="ltr">{pipe.detail}</span>
          </>
        )}
      </div>
    </li>
  );
};

const fromServer = (info: HaproxyInfo, s: HaproxyServer, multiBackend: boolean): Pipe => {
  const st = haproxyStatus(s.status);
  return {
    key: `${s.backend}/${s.name}`,
    name: multiBackend ? `${s.backend}/${s.name}` : s.name,
    addr: s.addr || s.name,
    status: s.status,
    tone: st.tone,
    kind: st.kind,
    backup: s.backup,
    active: isActiveServer(info, s),
    sessions: s.sessions,
    total: s.total_sessions,
    held: s.since_seconds,
  };
};

export const PathDiagram: FC<{
  tunnelId: number;
  stamp: unknown;
  nodeLabel: string;
  relayLabel: string;
  haproxy?: HaproxyInfo | null;
  direct?: DirectPath;
}> = ({ tunnelId, stamp, nodeLabel, relayLabel, haproxy, direct }) => {
  const { t } = useTranslation();
  const hasProxy = !!haproxy?.present;
  const servers = hasProxy ? haproxy?.servers ?? [] : [];
  const multi = new Set(servers.map((s) => s.backend)).size > 1;
  let pipes: Pipe[] = servers.map((s) => fromServer(haproxy as HaproxyInfo, s, multi));
  if (!hasProxy && direct) {
    const kind = direct.up === null ? "unknown" : direct.up ? "up" : "down";
    pipes = [
      {
        key: "direct",
        name: direct.name,
        addr: direct.addr,
        status: direct.up === null ? "—" : direct.up ? "UP" : "DOWN",
        tone: direct.up === null ? "gray" : direct.up ? "green" : "red",
        kind,
        backup: false,
        active: direct.up === true,
        held: direct.up === false ? direct.downForSeconds : undefined,
        detail: direct.up === false ? direct.error : undefined,
      },
    ];
  }
  const failover = hasProxy && failoverInEffect(haproxy as HaproxyInfo);
  const anyActive = pipes.some((p) => p.active);
  const title = hasProxy ? String(t("rapido.tunnels.haproxyTitle")) : String(t("rapido.tunnels.pathTitle"));

  return (
    <section className="flex flex-col gap-2.5" aria-label={title}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wider text-rapido-muted">{title}</h3>
        {hasProxy && !anyActive && <span className={TYPE.caption}>{String(t("rapido.tunnels.haproxyNoActive"))}</span>}
      </div>
      {failover && (
        <div role="alert" className="flex items-start gap-2 rounded-lg border border-rapido-warn/40 bg-rapido-warn/10 px-3 py-2 text-xs text-rapido-warn">
          <PulseDot tone="yellow" className="mt-1" />
          <span>{String(t("rapido.tunnels.haproxyFailover"))}</span>
        </div>
      )}
      {pipes.length === 0 ? (
        <p className={TYPE.caption}>{String(t("rapido.tunnels.haproxyNoServers"))}</p>
      ) : (
        <>
          {/* narrow screens: the stations as a row of chips above the pipes */}
          <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-rapido-muted md:hidden" aria-hidden="true">
            <Badge tone="gray">{nodeLabel}</Badge>
            <span className="rtl:rotate-180">→</span>
            {hasProxy && (
              <>
                <Badge tone="gray">HAProxy</Badge>
                <span className="rtl:rotate-180">→</span>
              </>
            )}
            <Badge tone="gray">{relayLabel}</Badge>
          </div>
          <div className="grid items-center gap-3 md:grid-cols-[auto_minmax(0,1fr)_auto]">
            <div className="hidden items-center gap-2 md:flex">
              <Station icon={<ServerIcon />} title={String(t("rapido.tunnels.nodeEnd"))} sub={nodeLabel} lit={anyActive} />
              {hasProxy && (
                <>
                  <div className="relative h-2.5 w-6 overflow-hidden rounded-full rtl:-scale-x-100 text-rapido-accent" aria-hidden="true" style={{ background: anyActive ? "rgb(var(--rp-accent) / 0.16)" : UNLIT }}>
                    {anyActive && <div className="rp-flow-dots animate-rp-flow" />}
                  </div>
                  <Station icon={<ArrowsRightLeftIcon />} title="HAProxy" lit={anyActive} />
                </>
              )}
            </div>
            <ul className="flex min-w-0 flex-col gap-2">
              {pipes.map((p) => (
                <PipeRow key={p.key} pipe={p} tunnelId={tunnelId} stamp={stamp} />
              ))}
            </ul>
            <div className="hidden md:block">
              <Station icon={<GlobeAltIcon />} title={String(t("rapido.tunnels.relayEnd"))} sub={relayLabel} lit={anyActive} />
            </div>
          </div>
        </>
      )}
    </section>
  );
};
