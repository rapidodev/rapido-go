import { FC, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import classNames from "classnames";
import {
  useCreateTunnelMutation,
  useDeleteTunnelMutation,
  useRestartTunnelMutation,
  useStartTunnelMutation,
  useStopTunnelMutation,
  useTunnelsQuery,
} from "hooks/useTunnelsQuery";
import { useNodesQuery } from "hooks/useNodesQuery";
import { useInboundPortsQuery } from "hooks/useInboundsQuery";
import { Tunnel, TunnelCreatePayload, TunnelStatus } from "types/Tunnel";
import { Node } from "types/Node";
import { parseInboundPortInput } from "utils/inboundPorts";
import { formatBytes } from "utils/formatByte";
import { errorText } from "service/errors";
import { Card } from "rapido-ui/Card";
import { Badge, BadgeTone } from "rapido-ui/Badge";
import { Button } from "rapido-ui/Button";
import { Input } from "rapido-ui/Input";
import { Select } from "rapido-ui/Select";
import { Checkbox } from "rapido-ui/Checkbox";
import { Modal } from "rapido-ui/Modal";

// Panel-provisioned GRE+FRP tunnels. A tunnel row IS its own monitoring
// entry (health via internal/relayhealth, resource metrics via
// internal/tunnelmetrics) - the old separate "Tunnel Relays" monitoring-only
// page was merged in here, since every real relay this fleet has is a
// provisioned tunnel and already carries the SSH credentials a resource
// check needs. Provisioning/stop/start/restart all run in the background on
// the server, so a row starts in a transient status and this page polls
// (useTunnelsQuery) until it settles.

const cardToneClasses: Partial<Record<BadgeTone, string>> = {
  green: "!border-emerald-500/60 bg-emerald-500/[0.04]",
  red: "!border-red-500/60 bg-red-500/[0.04]",
  yellow: "!border-yellow-500/50 bg-yellow-500/[0.03]",
};

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

// Reuses the exact "N <unit> ago" phrasing the Monitoring page already
// established.
const agoLabelKey = {
  seconds: "rapido.monitoring.agoSeconds",
  minutes: "rapido.monitoring.agoMinutes",
  hours: "rapido.monitoring.agoHours",
  days: "rapido.monitoring.agoDays",
} as const;

const agoText = (t: (key: string, opts?: Record<string, unknown>) => string, checkedAt: string): string => {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(checkedAt).getTime()) / 1000));
  if (seconds < 60) return t(agoLabelKey.seconds, { value: seconds });
  if (seconds < 3600) return t(agoLabelKey.minutes, { value: Math.floor(seconds / 60) });
  if (seconds < 86400) return t(agoLabelKey.hours, { value: Math.floor(seconds / 3600) });
  return t(agoLabelKey.days, { value: Math.floor(seconds / 86400) });
};

// ---------------------------------------------------------------------------

type FormValues = {
  name: string;
  nodeId: string;
  nodeSshPort: string;
  nodeSshUser: string;
  nodeSshPassword: string;
  relayHost: string;
  relaySshPort: string;
  relaySshUser: string;
  relaySshPassword: string;
  customPorts: string;
};

const emptyForm = (): FormValues => ({
  name: "",
  nodeId: "",
  nodeSshPort: "22",
  nodeSshUser: "root",
  nodeSshPassword: "",
  relayHost: "",
  relaySshPort: "22",
  relaySshUser: "root",
  relaySshPassword: "",
  customPorts: "",
});

// A node's own candidate ports: its explicit listen_ports profile if it has
// one, otherwise every port any inbound actually listens on (an
// unrestricted node serves all of them).
const candidatePortsForNode = (node: Node | undefined, portsByTag: Record<string, number[]>): number[] => {
  if (node?.listen_ports && node.listen_ports.length > 0) {
    return [...node.listen_ports].sort((a, b) => a - b);
  }
  const all = new Set<number>();
  for (const ports of Object.values(portsByTag)) for (const p of ports) all.add(p);
  return [...all].sort((a, b) => a - b);
};

const TunnelFormModal: FC<{ onClose: () => void; onCreated: () => void }> = ({ onClose, onCreated }) => {
  const { t } = useTranslation();
  const { data: nodes } = useNodesQuery();
  const { data: portsByTag } = useInboundPortsQuery();
  const [values, setValues] = useState<FormValues>(emptyForm());
  const [selectedPorts, setSelectedPorts] = useState<ReadonlySet<number>>(new Set());
  const [error, setError] = useState("");
  const createTunnel = useCreateTunnelMutation();

  const set = (patch: Partial<FormValues>) => setValues((v) => ({ ...v, ...patch }));

  const selectedNode = useMemo(
    () => (nodes ?? []).find((n) => String(n.id) === values.nodeId),
    [nodes, values.nodeId]
  );
  const candidatePorts = useMemo(
    () => candidatePortsForNode(selectedNode, portsByTag ?? {}),
    [selectedNode, portsByTag]
  );

  const togglePort = (port: number) =>
    setSelectedPorts((prev) => {
      const next = new Set(prev);
      if (next.has(port)) next.delete(port);
      else next.add(port);
      return next;
    });

  const customParsed = parseInboundPortInput(values.customPorts);
  const allPorts = useMemo(() => {
    const set = new Set(selectedPorts);
    if (customParsed.ok) for (const p of customParsed.ports) set.add(p);
    return [...set].sort((a, b) => a - b);
  }, [selectedPorts, customParsed]);

  const canSubmit =
    !!values.name.trim() &&
    !!values.nodeId &&
    !!values.relayHost.trim() &&
    !!values.relaySshUser.trim() &&
    !!values.relaySshPassword &&
    !!values.nodeSshUser.trim() &&
    !!values.nodeSshPassword &&
    customParsed.ok &&
    allPorts.length > 0;

  const submit = () => {
    setError("");
    if (!customParsed.ok) {
      setError(t("rapido.tunnels.customPortsInvalid"));
      return;
    }
    const body: TunnelCreatePayload = {
      name: values.name.trim(),
      node_id: Number(values.nodeId),
      node_ssh_port: Number(values.nodeSshPort) || 22,
      node_ssh_user: values.nodeSshUser.trim(),
      node_ssh_password: values.nodeSshPassword,
      relay_host: values.relayHost.trim(),
      relay_ssh_port: Number(values.relaySshPort) || 22,
      relay_ssh_user: values.relaySshUser.trim(),
      relay_ssh_password: values.relaySshPassword,
      ports: allPorts,
    };
    createTunnel.mutate(body, {
      onSuccess: onCreated,
      onError: (e) => setError(errorText(e, t("rapido.tunnels.saveFailed"))),
    });
  };

  return (
    <Modal onClose={onClose} title={t("rapido.tunnels.addTitle")}>
      <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto pe-1">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.name")}</span>
          <Input
            dir="ltr"
            placeholder={t("rapido.tunnels.namePlaceholder")}
            value={values.name}
            onChange={(e) => set({ name: e.target.value })}
          />
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.node")}</span>
          <Select
            value={values.nodeId}
            onChange={(e) => {
              set({ nodeId: e.target.value });
              setSelectedPorts(new Set());
            }}
          >
            <option value="">{t("rapido.tunnels.nodePlaceholder")}</option>
            {(nodes ?? []).map((n) => (
              <option key={n.id} value={n.id}>
                {n.name}
              </option>
            ))}
          </Select>
        </label>

        <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
          <span className="text-xs font-medium text-rapido-muted">{t("rapido.tunnels.relaySection")}</span>
          <label className="flex flex-col gap-1">
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.relayHost")}</span>
            <Input dir="ltr" value={values.relayHost} onChange={(e) => set({ relayHost: e.target.value })} />
          </label>
          <div className="grid grid-cols-3 gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPort")}</span>
              <Input
                dir="ltr"
                type="number"
                inputMode="numeric"
                value={values.relaySshPort}
                onChange={(e) => set({ relaySshPort: e.target.value })}
              />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshUser")}</span>
              <Input dir="ltr" value={values.relaySshUser} onChange={(e) => set({ relaySshUser: e.target.value })} />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPassword")}</span>
              <Input
                dir="ltr"
                type="password"
                value={values.relaySshPassword}
                onChange={(e) => set({ relaySshPassword: e.target.value })}
              />
            </label>
          </div>
        </div>

        <div className="flex flex-col gap-2 rounded-lg border border-rapido-border p-3">
          <span className="text-xs font-medium text-rapido-muted">{t("rapido.tunnels.nodeSection")}</span>
          <div className="grid grid-cols-3 gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPort")}</span>
              <Input
                dir="ltr"
                type="number"
                inputMode="numeric"
                value={values.nodeSshPort}
                onChange={(e) => set({ nodeSshPort: e.target.value })}
              />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshUser")}</span>
              <Input dir="ltr" value={values.nodeSshUser} onChange={(e) => set({ nodeSshUser: e.target.value })} />
            </label>
            <label className="col-span-1 flex flex-col gap-1">
              <span className="text-xs text-rapido-muted">{t("rapido.tunnels.sshPassword")}</span>
              <Input
                dir="ltr"
                type="password"
                value={values.nodeSshPassword}
                onChange={(e) => set({ nodeSshPassword: e.target.value })}
              />
            </label>
          </div>
        </div>

        <div className="flex flex-col gap-2">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.ports")}</span>
          {!values.nodeId ? (
            <p className="text-xs text-rapido-muted">{t("rapido.tunnels.portsNeedsNode")}</p>
          ) : candidatePorts.length === 0 ? (
            <p className="text-xs text-rapido-muted">{t("rapido.tunnels.portsEmpty")}</p>
          ) : (
            <div className="flex flex-wrap gap-3 rounded-lg border border-rapido-border p-3">
              {candidatePorts.map((port) => (
                <Checkbox
                  key={port}
                  checked={selectedPorts.has(port)}
                  onChange={() => togglePort(port)}
                  label={<span dir="ltr">{port}</span>}
                />
              ))}
            </div>
          )}
          <label className="flex flex-col gap-1">
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.customPorts")}</span>
            <Input
              dir="ltr"
              placeholder="20001, 20005, 20009"
              value={values.customPorts}
              onChange={(e) => set({ customPorts: e.target.value })}
            />
            <span className="text-xs text-rapido-muted">{t("rapido.tunnels.customPortsHint")}</span>
          </label>
          {!customParsed.ok && values.customPorts.trim() !== "" && (
            <span className="text-xs text-red-400">{t("rapido.tunnels.customPortsInvalid")}</span>
          )}
        </div>

        {error && (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
            {error}
          </div>
        )}

        <div className="mt-2 flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={createTunnel.isPending}>
            {t("cancel")}
          </Button>
          <Button variant="primary" disabled={createTunnel.isPending || !canSubmit} onClick={submit}>
            {createTunnel.isPending ? t("rapido.pleaseWait") : t("rapido.tunnels.addTitle")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};

// ---------------------------------------------------------------------------

const HealthRow: FC<{ tunnel: Tunnel }> = ({ tunnel }) => {
  const { t } = useTranslation();
  const health = tunnel.health;
  const tone: BadgeTone = health === null ? "gray" : health.up ? "green" : "red";
  const label = health === null
    ? t("rapido.tunnels.healthUnknown")
    : t(health.up ? "rapido.tunnels.healthUp" : "rapido.tunnels.healthDown");

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <span className="text-rapido-muted">{t("rapido.tunnels.health")}:</span>
      <Badge tone={tone} title={health?.error || undefined}>
        {label}
      </Badge>
      {health?.checked_at && <span className="text-rapido-muted">{agoText(t, health.checked_at)}</span>}
    </div>
  );
};

const MetricBar: FC<{ label: string; used: number; total: number; display: string }> = ({
  label,
  used,
  total,
  display,
}) => {
  const pct = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex items-center justify-between text-[11px] text-rapido-muted">
        <span>{label}</span>
        <span dir="ltr">{display}</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-rapido-raised">
        <div
          className={classNames("h-full rounded-full", pct >= 90 ? "bg-red-500" : pct >= 70 ? "bg-amber-500" : "bg-rapido-accent")}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
};

const MetricsSection: FC<{ tunnel: Tunnel }> = ({ tunnel }) => {
  const { t } = useTranslation();
  const m = tunnel.metrics;
  if (!m) {
    return <p className="text-xs text-rapido-muted">{t("rapido.tunnels.metricsPending")}</p>;
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="grid grid-cols-2 gap-3">
        <MetricBar
          label={t("rapido.tunnels.metricsCpu")}
          used={m.cpu_percent}
          total={100}
          display={`${m.cpu_percent}%`}
        />
        <MetricBar
          label={t("rapido.tunnels.metricsRam")}
          used={m.mem_used_mb}
          total={m.mem_total_mb}
          display={`${m.mem_used_mb} / ${m.mem_total_mb} MB`}
        />
        <MetricBar
          label={t("rapido.tunnels.metricsDisk")}
          used={m.disk_used_gb}
          total={m.disk_total_gb}
          display={`${m.disk_used_gb} / ${m.disk_total_gb} GB`}
        />
        <div className="flex flex-col gap-0.5">
          <span className="text-[11px] text-rapido-muted">{t("rapido.tunnels.metricsTraffic")}</span>
          <span className="text-xs" dir="ltr">
            ↓{formatBytes(m.rx_bytes)} / ↑{formatBytes(m.tx_bytes)}
          </span>
        </div>
      </div>
      <span className="text-[11px] text-rapido-muted">{t("rapido.tunnels.lastChecked", { time: agoText(t, m.checked_at) })}</span>
    </div>
  );
};

// ---------------------------------------------------------------------------

const TunnelCard: FC<{ tunnel: Tunnel; nodeName: string | undefined }> = ({ tunnel, nodeName }) => {
  const { t } = useTranslation();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const deleteTunnel = useDeleteTunnelMutation();
  const stopTunnel = useStopTunnelMutation();
  const startTunnel = useStartTunnelMutation();
  const restartTunnel = useRestartTunnelMutation();
  const tone = statusTone[tunnel.status];
  const busy = BUSY_STATUSES.has(tunnel.status) || deleteTunnel.isPending || stopTunnel.isPending || startTunnel.isPending || restartTunnel.isPending;

  const runAction = (mutate: (id: number, opts: { onError: (e: unknown) => void }) => void, failKey: string) => {
    setMsg(null);
    mutate(tunnel.id, { onError: (e) => setMsg(errorText(e, t(failKey))) });
  };

  const remove = () => {
    setMsg(null);
    deleteTunnel.mutate(tunnel.id, {
      onSuccess: (result) => {
        if (result.teardown_warning) setMsg(result.teardown_warning);
      },
      onError: (e) => {
        setMsg(errorText(e, t("rapido.tunnels.deleteFailed")));
        setConfirmDelete(false);
      },
    });
  };

  return (
    <Card className={classNames("p-4", cardToneClasses[tone])}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-semibold">{tunnel.name}</span>
            <Badge tone={tone}>{t(statusLabelKey[tunnel.status])}</Badge>
          </div>
        </div>

        <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-rapido-muted">
          <span>
            {t("rapido.tunnels.node")}: <span className="text-rapido-text">{nodeName ?? tunnel.node_id}</span>
          </span>
          <span>
            {t("rapido.tunnels.relayHost")}:{" "}
            <span className="text-rapido-text" dir="ltr">
              {tunnel.relay_host}
            </span>
          </span>
        </div>

        <div className="flex flex-col gap-1">
          <span className="text-xs text-rapido-muted">{t("rapido.tunnels.portsLabel")} ({tunnel.ports.length})</span>
          <div className="flex flex-wrap gap-1" dir="ltr">
            {tunnel.ports.map((port) => (
              <span key={port} className="rounded bg-rapido-raised px-1.5 py-0.5 text-[11px] text-rapido-text">
                {port}
              </span>
            ))}
          </div>
        </div>

        <HealthRow tunnel={tunnel} />
        <MetricsSection tunnel={tunnel} />

        {tunnel.status === "failed" && tunnel.status_message && (
          <div className="break-words text-xs text-rapido-muted" title={tunnel.status_message}>
            <span dir="ltr" className="text-red-300/80">
              {tunnel.status_message}
            </span>
          </div>
        )}

        {msg && (
          <div className="rounded-lg border border-yellow-500/30 bg-yellow-500/10 px-3 py-2 text-xs text-yellow-300">
            {msg}
          </div>
        )}

        <div className="flex flex-wrap items-center gap-1.5">
          {!busy && tunnel.status === "active" && (
            <>
              <Button
                variant="chip"
                tone="sky"
                onClick={() => runAction((id, o) => restartTunnel.mutate(id, o), "rapido.tunnels.restartFailed")}
              >
                {t("rapido.tunnels.restart")}
              </Button>
              <Button
                variant="chip"
                tone="amber"
                onClick={() => runAction((id, o) => stopTunnel.mutate(id, o), "rapido.tunnels.stopFailed")}
              >
                {t("rapido.tunnels.stop")}
              </Button>
            </>
          )}
          {!busy && (tunnel.status === "stopped" || tunnel.status === "failed") && (
            <Button
              variant="chip"
              tone="accent"
              onClick={() => runAction((id, o) => startTunnel.mutate(id, o), "rapido.tunnels.startFailed")}
            >
              {t("rapido.tunnels.start")}
            </Button>
          )}
          {confirmDelete ? (
            <>
              <span className="text-xs text-red-400">{t("rapido.tunnels.confirmDelete")}</span>
              <Button variant="chip" tone="red" disabled={deleteTunnel.isPending} onClick={remove}>
                {t("delete")}
              </Button>
              <Button variant="chip" onClick={() => setConfirmDelete(false)}>
                {t("cancel")}
              </Button>
            </>
          ) : (
            <Button variant="chip" tone="red" disabled={busy} onClick={() => setConfirmDelete(true)}>
              {t("delete")}
            </Button>
          )}
        </div>
      </div>
    </Card>
  );
};

// ---------------------------------------------------------------------------

export const TunnelsAdmin: FC = () => {
  const { t } = useTranslation();
  const { data: tunnels, isLoading, isError } = useTunnelsQuery();
  const { data: nodes } = useNodesQuery();
  const [adding, setAdding] = useState(false);

  const rows = tunnels ?? [];
  const active = rows.filter((r) => r.status === "active").length;
  const nodeNameById = useMemo(() => {
    const map = new Map<number, string>();
    for (const n of nodes ?? []) map.set(n.id, n.name);
    return map;
  }, [nodes]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-sm text-rapido-muted">{t("rapido.tunnels.summary", { active, total: rows.length })}</div>
        <Button variant="chip" tone="accent" onClick={() => setAdding(true)}>
          + {t("rapido.tunnels.addNew")}
        </Button>
      </div>

      {isError && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {t("rapido.tunnels.loadFailed")}
        </div>
      )}

      {isLoading ? (
        <p className="text-sm text-rapido-muted">{t("rapido.tickets.loading")}</p>
      ) : rows.length === 0 ? (
        <Card className="p-6 text-center text-sm text-rapido-muted">{t("rapido.tunnels.empty")}</Card>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          {rows.map((tunnel) => (
            <TunnelCard key={tunnel.id} tunnel={tunnel} nodeName={nodeNameById.get(tunnel.node_id)} />
          ))}
        </div>
      )}

      {adding && <TunnelFormModal onClose={() => setAdding(false)} onCreated={() => setAdding(false)} />}
    </div>
  );
};

export default TunnelsAdmin;
