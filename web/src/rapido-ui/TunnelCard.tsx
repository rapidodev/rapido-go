import { FC, ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import classNames from "classnames";
import {
  useDeleteTunnelMutation,
  useRestartTunnelMutation,
  useStartTunnelMutation,
  useStopTunnelMutation,
} from "hooks/useTunnelsQuery";
import { Tunnel, TunnelStatus } from "types/Tunnel";
import { activeServerOf, formatDuration, ipFamily } from "utils/tunnelMetrics";
import { agoText } from "utils/ago";
import { errorText } from "service/errors";
import { Badge, BadgeTone } from "./Badge";
import { ltrIsolate } from "./bidi";
import { Button } from "./Button";
import { Card } from "./Card";
import { PathDiagram } from "./PathDiagram";
import { StatusPill } from "./StatusPill";
import { TunnelCluster } from "./TunnelGauges";
import { TYPE } from "./instrument";

const statusTone: Record<TunnelStatus, BadgeTone> = {
  pending: "yellow",
  active: "green",
  failed: "red",
  deleting: "yellow",
  stopped: "gray",
  stopping: "yellow",
  starting: "yellow",
};

const statusLabelKey: Record<TunnelStatus, string> = {
  pending: "rapido.tunnels.statusPending",
  active: "rapido.tunnels.statusActive",
  failed: "rapido.tunnels.statusFailed",
  deleting: "rapido.tunnels.statusDeleting",
  stopped: "rapido.tunnels.statusStopped",
  stopping: "rapido.tunnels.statusStopping",
  starting: "rapido.tunnels.statusStarting",
};

const BUSY_STATUSES = new Set<TunnelStatus>(["pending", "deleting", "stopping", "starting"]);

/** Forwarded ports as chips; long lists collapse to "+N". */
const PortsRow: FC<{ ports: readonly number[] }> = ({ ports }) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const LIMIT = 8;
  const shown = open ? ports : ports.slice(0, LIMIT);
  const more = ports.length - shown.length;
  const chip = "rounded-md bg-rapido-raised px-1.5 py-0.5 text-[11px] tabular-nums text-rapido-text ring-1 ring-inset ring-white/10";
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className={TYPE.caption}>
        {String(t("rapido.tunnels.portsLabel"))} ({ports.length})
      </span>
      <div className="flex flex-wrap gap-1" dir="ltr">
        {shown.map((p) => (
          <span key={p} className={chip}>
            {p}
          </span>
        ))}
        {more > 0 && (
          <button type="button" onClick={() => setOpen(true)} className={classNames(chip, "text-rapido-accent hover:bg-rapido-accent/15")} title={ports.slice(LIMIT).join(", ")}>
            +{more}
          </button>
        )}
        {open && ports.length > LIMIT && (
          <button type="button" onClick={() => setOpen(false)} className={classNames(chip, "text-rapido-muted hover:text-rapido-text")}>
            −
          </button>
        )}
      </div>
    </div>
  );
};

export const TunnelCard: FC<{
  tunnel: Tunnel;
  nodeName: string | undefined;
  onEdit: (tunnel: Tunnel) => void;
  /** The tunnel was deleted but a server could not be cleaned up. It goes to the page:
   * this card is gone by the time the answer is in. */
  onTeardownWarning: (name: string, warning: string) => void;
  /** Room for an events / outage timeline strip under the path. */
  events?: ReactNode;
}> = ({ tunnel, nodeName, onEdit, onTeardownWarning, events }) => {
  const { t } = useTranslation();
  const [confirm, setConfirm] = useState<"delete" | "restart" | "stop" | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const deleteTunnel = useDeleteTunnelMutation();
  const stopTunnel = useStopTunnelMutation();
  const startTunnel = useStartTunnelMutation();
  const restartTunnel = useRestartTunnelMutation();
  const tone = statusTone[tunnel.status];
  // Only an explicit false marks a detected tunnel; a backend that predates the
  // field has none, and every tunnel then is one the panel built.
  const managed = tunnel.managed !== false;
  const busy =
    BUSY_STATUSES.has(tunnel.status) ||
    deleteTunnel.isPending ||
    stopTunnel.isPending ||
    startTunnel.isPending ||
    restartTunnel.isPending;
  const m = tunnel.metrics;
  const haproxy = m?.node?.haproxy;
  const displayName = nodeName ?? String(tunnel.node_id);
  const health = tunnel.health;

  // Which IP family the traffic is using right now.
  const activeAddr = haproxy?.present ? activeServerOf(haproxy)?.addr : tunnel.relay_tunnel_addr || tunnel.relay_host;
  const family = ipFamily(activeAddr);
  const methodLabel = String(t(`rapido.tunnels.method.${tunnel.method}`));

  const runAction = (mutate: (id: number, opts: { onError: (e: unknown) => void }) => void, failKey: string) => {
    setMsg(null);
    mutate(tunnel.id, { onError: (e) => setMsg(errorText(e, String(t(failKey)))) });
  };

  // The list is refetched before the delete settles, so this card is on its way out
  // when the answer arrives: a warning kept here would never be seen. mutateAsync
  // because, unlike mutate's callbacks, it still answers after an unmount.
  const remove = () => {
    setMsg(null);
    deleteTunnel.mutateAsync(tunnel.id).then(
      (result) => {
        if (result.teardown_warning) onTeardownWarning(tunnel.name, result.teardown_warning);
      },
      (e) => {
        setMsg(errorText(e, String(t("rapido.tunnels.deleteFailed"))));
        setConfirm(null);
      }
    );
  };

  const runConfirmed = () => {
    if (confirm === "delete") {
      remove();
      return;
    }
    setConfirm(null);
    if (confirm === "restart") runAction((id, o) => restartTunnel.mutate(id, o), "rapido.tunnels.restartFailed");
    else runAction((id, o) => stopTunnel.mutate(id, o), "rapido.tunnels.stopFailed");
  };

  const confirmText = () => {
    if (confirm === "delete" && !managed) return t("rapido.tunnels.confirmDeleteDetected");
    const text = t(confirm === "delete" ? "rapido.tunnels.confirmDelete" : confirm === "restart" ? "rapido.tunnels.confirmRestart" : "rapido.tunnels.confirmStop");
    // Live count of established connections through this tunnel, once it has been measured.
    return m ? `${text} ${t("rapido.tunnels.openConnsNow", { value: ltrIsolate(m.connections) })}` : text;
  };

  return (
    <Card tilt tiltMax={1.5} className="p-0" data-testid="tunnel-card">
      <div className="flex flex-col gap-4 p-4 sm:p-5">
        {/* header: identity and route on the left, actions grouped at the end */}
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
          <div className="flex min-w-0 flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="truncate text-base font-semibold text-rapido-text">{tunnel.name}</span>
              <StatusPill tone={tone} live={tunnel.status === "active" || BUSY_STATUSES.has(tunnel.status)}>
                {t(statusLabelKey[tunnel.status])}
              </StatusPill>
              {!managed && (
                <Badge tone="sky" title={String(t("rapido.tunnels.detectedHint"))}>
                  {t("rapido.tunnels.detected")}
                </Badge>
              )}
              {tunnel.protocol ? (
                <Badge tone="violet" className="max-w-full whitespace-normal" data-testid="protocol-chip">
                  {tunnel.protocol}
                </Badge>
              ) : (
                tunnel.method !== "external" && (
                  <Badge tone="violet" data-testid="protocol-chip">
                    {methodLabel}
                  </Badge>
                )
              )}
            </div>
            <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-rapido-muted">
              <span className="text-rapido-text">{displayName}</span>
              <span aria-hidden="true" className="rtl:rotate-180">
                →
              </span>
              <span className="text-rapido-text" dir="ltr">
                {tunnel.relay_host}
              </span>
              {family && (
                <Badge tone="gray" className="px-1.5 py-0.5 text-[10px] font-semibold" title={String(t("rapido.tunnels.activeFamily"))}>
                  {family}
                </Badge>
              )}
              <span aria-hidden="true">·</span>
              <span className="flex items-center gap-1.5">
                <StatusPill
                  tone={health === null ? "gray" : health.up ? "green" : "red"}
                  live={health?.up === true}
                  title={health?.error || undefined}
                  className="px-2 py-0.5 text-[11px]"
                >
                  {t("rapido.tunnels.health")}:{" "}
                  {health === null ? t("rapido.tunnels.healthUnknown") : t(health.up ? "rapido.tunnels.healthUp" : "rapido.tunnels.healthDown")}
                </StatusPill>
                {!health?.up && health?.down_for_seconds !== undefined && (
                  <span dir="ltr">{t("rapido.tunnels.downFor", { time: formatDuration(health.down_for_seconds) })}</span>
                )}
                {health?.checked_at && <span>{agoText(t, health.checked_at)}</span>}
              </span>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-1.5">
            {confirm ? (
              <>
                <span className="max-w-[20rem] text-xs text-rapido-bad">{confirmText()}</span>
                <Button variant="chip" tone="red" disabled={deleteTunnel.isPending} onClick={runConfirmed}>
                  {confirm === "restart"
                    ? t("rapido.tunnels.restart")
                    : confirm === "stop"
                      ? t("rapido.tunnels.stop")
                      : managed
                        ? t("delete")
                        : t("rapido.tunnels.removeFromPanel")}
                </Button>
                <Button variant="chip" onClick={() => setConfirm(null)}>
                  {t("cancel")}
                </Button>
              </>
            ) : (
              <>
                <Button variant="chip" tone="accent" disabled={busy} onClick={() => onEdit(tunnel)}>
                  {t("rapido.tunnels.edit")}
                </Button>
                {managed && !busy && tunnel.status === "active" && (
                  <>
                    <Button variant="chip" tone="sky" onClick={() => setConfirm("restart")}>
                      {t("rapido.tunnels.restart")}
                    </Button>
                    <Button variant="chip" tone="amber" onClick={() => setConfirm("stop")}>
                      {t("rapido.tunnels.stop")}
                    </Button>
                  </>
                )}
                {managed && !busy && (tunnel.status === "stopped" || tunnel.status === "failed") && (
                  <Button variant="chip" tone="accent" onClick={() => runAction((id, o) => startTunnel.mutate(id, o), "rapido.tunnels.startFailed")}>
                    {t("rapido.tunnels.start")}
                  </Button>
                )}
                <Button variant="chip" tone="red" disabled={busy} onClick={() => setConfirm("delete")}>
                  {managed ? t("delete") : t("rapido.tunnels.removeFromPanel")}
                </Button>
              </>
            )}
          </div>
        </div>

        {tunnel.status === "failed" && tunnel.status_message && (
          <div className="break-words text-xs text-rapido-muted" title={tunnel.status_message}>
            <span dir="ltr" className="text-rapido-bad/90">
              {tunnel.status_message}
            </span>
          </div>
        )}

        {msg && (
          <div className="rounded-lg border border-rapido-warn/30 bg-rapido-warn/10 px-3 py-2 text-xs text-rapido-warn">{msg}</div>
        )}

        {/* the instrument cluster, then the path the traffic takes */}
        {m ? (
          <>
            <TunnelCluster tunnel={tunnel} metrics={m} nodeName={displayName} />
            <PathDiagram
              tunnelId={tunnel.id}
              stamp={m.checked_at}
              nodeLabel={displayName}
              relayLabel={tunnel.relay_host}
              haproxy={haproxy}
              direct={{
                name: tunnel.protocol ?? methodLabel,
                addr: tunnel.relay_tunnel_addr || tunnel.relay_host,
                up: health ? health.up : null,
                error: health?.error,
                downForSeconds: health?.down_for_seconds,
              }}
            />
          </>
        ) : (
          <p className={TYPE.caption}>{t("rapido.tunnels.metricsPending")}</p>
        )}

        {events}

        {/* footer: ports, and when the numbers were taken */}
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-rapido-border/60 pt-3">
          <PortsRow ports={tunnel.ports} />
          {m && <span className={TYPE.caption}>{t("rapido.tunnels.lastChecked", { time: agoText(t, m.checked_at) })}</span>}
        </div>
      </div>
    </Card>
  );
};
